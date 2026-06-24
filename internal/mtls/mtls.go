// Package mtls turns a password-protected PKCS#12 bundle into the TLS material
// needed to authenticate to the upstream endpoint, and assembles the upstream
// tls.Config (client certificate + trust roots).
package mtls

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"

	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

// LoadClientCertificate decodes a password-protected .p12 into a tls.Certificate,
// preserving any intermediate certificates in the chain.
func LoadClientCertificate(p12Path, password string) (tls.Certificate, error) {
	data, err := os.ReadFile(p12Path)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("read p12: %w", err)
	}
	key, leaf, cas, err := pkcs12.DecodeChain(data, password)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("decode p12 (wrong password?): %w", err)
	}
	cert := tls.Certificate{PrivateKey: key, Leaf: leaf}
	cert.Certificate = append(cert.Certificate, leaf.Raw)
	for _, ca := range cas {
		cert.Certificate = append(cert.Certificate, ca.Raw)
	}
	return cert, nil
}

// ExtractPEM decodes the .p12 and writes the leaf certificate (plus any
// intermediates) and the private key as PEM files. The private key is encoded as
// PKCS#8 ("PRIVATE KEY"); the key file is created with 0600 permissions.
func ExtractPEM(p12Path, password, certOut, keyOut string) error {
	data, err := os.ReadFile(p12Path)
	if err != nil {
		return fmt.Errorf("read p12: %w", err)
	}
	key, leaf, cas, err := pkcs12.DecodeChain(data, password)
	if err != nil {
		return fmt.Errorf("decode p12 (wrong password?): %w", err)
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})
	for _, ca := range cas {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})...)
	}

	if err := os.WriteFile(certOut, certPEM, 0o644); err != nil {
		return fmt.Errorf("write cert: %w", err)
	}
	if err := os.WriteFile(keyOut, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	return nil
}

// BuildTLSConfig assembles the upstream tls.Config. System roots form the base;
// embeddedCA (compiled in) and an optional extra PEM file are appended. A client
// certificate, when supplied, is presented for mutual TLS.
func BuildTLSConfig(cert *tls.Certificate, embeddedCA []byte, extraCAPath string) (*tls.Config, error) {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if len(embeddedCA) > 0 {
		roots.AppendCertsFromPEM(embeddedCA)
	}
	if extraCAPath != "" {
		pemBytes, err := os.ReadFile(extraCAPath)
		if err != nil {
			return nil, fmt.Errorf("read ca bundle %s: %w", extraCAPath, err)
		}
		if !roots.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("no certificates found in %s", extraCAPath)
		}
	}
	cfg := &tls.Config{
		RootCAs:    roots,
		MinVersion: tls.VersionTLS12,
	}
	if cert != nil && len(cert.Certificate) > 0 {
		cfg.Certificates = []tls.Certificate{*cert}
	}
	return cfg, nil
}
