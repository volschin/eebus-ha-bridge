package certs

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"

	shipcert "github.com/enbility/ship-go/cert"
)

func TestEnsureCertificateRejectsInvalidExplicitAndStoredPairs(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.pem")
	if _, err := EnsureCertificate(missing, missing, "", false); err == nil {
		t.Fatal("missing explicit certificate pair was accepted")
	}

	dir := t.TempDir()
	for _, name := range []string{"cert.pem", "key.pem"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("not PEM"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := EnsureCertificate("", "", dir, false); err == nil {
		t.Fatal("invalid stored certificate pair was accepted")
	}
}

func TestEnsureCertificateReportsPersistenceFailure(t *testing.T) {
	storagePath := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(storagePath, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureCertificate("", "", storagePath, true); err == nil || !strings.Contains(err.Error(), "persisting certificate") {
		t.Fatalf("error = %v, want persistence failure", err)
	}
}

func TestPersistCertificateReportsCertificateAndKeyOpenErrors(t *testing.T) {
	certificate, err := shipcert.CreateCertificate("test", "test", "DE", "cert-test")
	if err != nil {
		t.Fatal(err)
	}

	certBlocked := t.TempDir()
	if err := os.Mkdir(filepath.Join(certBlocked, "cert.pem"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := persistCertificate(certificate, certBlocked); err == nil {
		t.Fatal("certificate output directory was accepted as a file")
	}

	keyBlocked := t.TempDir()
	if err := os.Mkdir(filepath.Join(keyBlocked, "key.pem"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := persistCertificate(certificate, keyBlocked); err == nil {
		t.Fatal("key output directory was accepted as a file")
	}
}

func TestSKIFromCertificateRejectsEmptyAndInvalidDER(t *testing.T) {
	if _, err := SKIFromCertificate(tls.Certificate{}); err == nil || !strings.Contains(err.Error(), "empty certificate") {
		t.Fatalf("empty certificate error = %v", err)
	}
	invalid := tls.Certificate{Certificate: [][]byte{[]byte("not DER")}}
	if _, err := SKIFromCertificate(invalid); err == nil || !strings.Contains(err.Error(), "parsing certificate") {
		t.Fatalf("invalid DER error = %v", err)
	}
}
