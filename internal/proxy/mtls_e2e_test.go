package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hunchom/claude-code-gateway/internal/config"
)

func genCert(t *testing.T, cn string, serial int64, parent *x509.Certificate, parentKey *ecdsa.PrivateKey, isCA bool, ips []net.IP) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  isCA,
		IPAddresses:           ips,
	}
	signer, signerKey := parent, parentKey
	if signer == nil { // self-signed
		signer, signerKey = tmpl, key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key, der
}

// TestGatewayMutualTLS exercises the core feature: ccgate authenticating to an
// upstream that REQUIRES a client certificate. It generates a CA, a server cert,
// and a client cert in-process (no external tools), stands up an mTLS server, and
// verifies a count_tokens passthrough succeeds only because the client cert is
// presented.
func TestGatewayMutualTLS(t *testing.T) {
	caCert, caKey, _ := genCert(t, "ccgate-test-ca", 1, nil, nil, true, nil)
	srvCert, srvKey, srvDER := genCert(t, "127.0.0.1", 2, caCert, caKey, false, []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback})
	cliCert, cliKey, cliDER := genCert(t, "ccgate-client", 3, caCert, caKey, false, nil)

	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)

	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens":7}`))
	}))
	upstream.TLS = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{srvDER}, PrivateKey: srvKey, Leaf: srvCert}},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
	}
	upstream.StartTLS()
	defer upstream.Close()

	clientTLS := &tls.Config{
		RootCAs:      caPool,
		Certificates: []tls.Certificate{{Certificate: [][]byte{cliDER}, PrivateKey: cliKey, Leaf: cliCert}},
	}
	cfg := config.Default()
	cfg.Upstream = upstream.URL
	cfg.CountTokens = config.CountPassthrough
	cfg.StateDir = t.TempDir()
	gw, err := New(cfg, clientTLS)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(gw)
	defer front.Close()

	resp, err := http.Post(front.URL+"/v1/messages/count_tokens", "application/json",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"input_tokens":7`) {
		t.Fatalf("mTLS passthrough failed: status=%d body=%q", resp.StatusCode, b)
	}
}
