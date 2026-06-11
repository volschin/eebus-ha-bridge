package certs

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	shipcert "github.com/enbility/ship-go/cert"
)

// EnsureCertificate returns a TLS certificate using this priority:
// 1. Explicit certFile + keyFile if both set
// 2. Existing cert.pem + key.pem in storagePath
// 3. Auto-generate via ship-go, persist in storagePath
func EnsureCertificate(certFile, keyFile, storagePath string) (tls.Certificate, error) {
	// Priority 1: explicit files
	if certFile != "" && keyFile != "" {
		return tls.LoadX509KeyPair(certFile, keyFile)
	}

	// Priority 2: existing files in storagePath
	if storagePath != "" {
		certPath := filepath.Join(storagePath, "cert.pem")
		keyPath := filepath.Join(storagePath, "key.pem")
		if fileExists(certPath) && fileExists(keyPath) {
			return tls.LoadX509KeyPair(certPath, keyPath)
		}
	}

	// Priority 3: auto-generate
	cert, err := shipcert.CreateCertificate("eebus-bridge", "eebus-bridge", "DE", "eebus-bridge")
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generating certificate: %w", err)
	}

	// Persist if storagePath set
	if storagePath != "" {
		if err := persistCertificate(cert, storagePath); err != nil {
			return tls.Certificate{}, fmt.Errorf("persisting certificate: %w", err)
		}
	}

	return cert, nil
}

// SKIFromCertificate extracts the Subject Key Identifier from a TLS certificate.
func SKIFromCertificate(cert tls.Certificate) (string, error) {
	if len(cert.Certificate) == 0 {
		return "", fmt.Errorf("empty certificate")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return "", fmt.Errorf("parsing certificate: %w", err)
	}
	return shipcert.SkiFromCertificate(leaf)
}

func persistCertificate(cert tls.Certificate, dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	// Write cert PEM
	certOut, err := os.Create(filepath.Join(dir, "cert.pem")) // #nosec G304 -- dir is an operator-controlled config path, not user input
	if err != nil {
		return err
	}
	defer func() { _ = certOut.Close() }()
	for _, c := range cert.Certificate {
		if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: c}); err != nil {
			return err
		}
	}

	// Write key PEM
	keyBytes, err := x509.MarshalECPrivateKey(cert.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		return err
	}
	keyOut, err := os.OpenFile(filepath.Join(dir, "key.pem"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600) // #nosec G304 -- dir is an operator-controlled config path, not user input
	if err != nil {
		return err
	}
	defer func() { _ = keyOut.Close() }()
	return pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
