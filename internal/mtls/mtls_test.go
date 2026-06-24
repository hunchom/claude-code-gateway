package mtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

// makeP12 builds a self-signed client certificate and encodes it as a
// password-protected PKCS#12 file, returning its path.
func makeP12(t *testing.T, password string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ccgate-test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pfx, err := pkcs12.Modern.Encode(key, cert, nil, password)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "client.p12")
	if err := os.WriteFile(path, pfx, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadClientCertificate(t *testing.T) {
	p12 := makeP12(t, "s3cret")
	cert, err := LoadClientCertificate(p12, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if len(cert.Certificate) == 0 || cert.PrivateKey == nil {
		t.Fatal("loaded certificate is empty")
	}
	if _, err := LoadClientCertificate(p12, "wrong-password"); err == nil {
		t.Error("expected an error with the wrong password")
	}
}

func TestExtractPEM(t *testing.T) {
	p12 := makeP12(t, "pw")
	dir := t.TempDir()
	certOut := filepath.Join(dir, "user-cert.pem")
	keyOut := filepath.Join(dir, "user-key.pem")
	if err := ExtractPEM(p12, "pw", certOut, keyOut); err != nil {
		t.Fatal(err)
	}

	certPEM, err := os.ReadFile(certOut)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(keyOut)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Fatalf("extracted PEMs are not a valid keypair: %v", err)
	}

	info, err := os.Stat(keyOut)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file mode = %o, want 600", perm)
	}
}

func TestBuildTLSConfig(t *testing.T) {
	cfg, err := BuildTLSConfig(nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("min version = %x, want TLS 1.2", cfg.MinVersion)
	}
	if len(cfg.Certificates) != 0 {
		t.Error("expected no client certificate when none supplied")
	}
}
