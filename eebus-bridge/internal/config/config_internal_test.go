package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApplyEnvOverridesCoversEverySupportedValue(t *testing.T) {
	values := map[string]string{
		"EEBUS_GRPC_PORT":          "50052",
		"EEBUS_GRPC_BIND":          "localhost",
		"EEBUS_GRPC_REFLECTION":    "true",
		"EEBUS_GRPC_SECURITY_MODE": "tls_token",
		"EEBUS_GRPC_TLS_CERT_FILE": "/tls/cert.pem",
		"EEBUS_GRPC_TLS_KEY_FILE":  "/tls/key.pem",
		"EEBUS_GRPC_TOKEN_FILE":    "/tls/token",
		"EEBUS_PORT":               "4713",
		"EEBUS_VENDOR":             "Vendor",
		"EEBUS_BRAND":              "Brand",
		"EEBUS_MODEL":              "Model",
		"EEBUS_SERIAL":             "Serial",
		"EEBUS_CERT_FILE":          "/eebus/cert.pem",
		"EEBUS_KEY_FILE":           "/eebus/key.pem",
		"EEBUS_CERT_STORAGE":       "/eebus/storage",
		"EEBUS_DEBUG_EVENTS":       "true",
		"EEBUS_SHIP_LOG":           "true",
		"EEBUS_SHIP_TRACE":         "true",
		"EEBUS_EXP_MGCP_PROVIDER":  "true",
		"EEBUS_EXP_VAPD_PROVIDER":  "true",
		"EEBUS_EXP_VABD_PROVIDER":  "true",
		"EEBUS_OHPCF_ENABLED":      "false",
		"EEBUS_EXP_TRUST_SKI":      "AABBCCDDEEFF00112233445566778899AABBCCDD",
	}
	for name, value := range values {
		t.Setenv(name, value)
	}

	cfg := defaultConfig()
	applied, err := applyEnvOverrides(cfg)
	if err != nil {
		t.Fatalf("applyEnvOverrides: %v", err)
	}
	if len(applied) != len(values) {
		t.Fatalf("applied overrides = %d, want %d: %v", len(applied), len(values), applied)
	}
	if cfg.GRPC.Port != 50052 || cfg.GRPC.Bind != "localhost" || !cfg.GRPC.EnableReflection {
		t.Fatalf("gRPC overrides = %+v", cfg.GRPC)
	}
	if cfg.GRPC.Security.Mode != GRPCSecurityModeTLSToken || cfg.GRPC.Security.TokenFile != "/tls/token" {
		t.Fatalf("security overrides = %+v", cfg.GRPC.Security)
	}
	if cfg.EEBUS.Port != 4713 || cfg.EEBUS.Vendor != "Vendor" || cfg.EEBUS.Serial != "Serial" {
		t.Fatalf("EEBUS overrides = %+v", cfg.EEBUS)
	}
	if cfg.Certificates.CertFile != "/eebus/cert.pem" || cfg.Certificates.StoragePath != "/eebus/storage" {
		t.Fatalf("certificate overrides = %+v", cfg.Certificates)
	}
	if !cfg.Logging.DebugEvents || !cfg.Logging.ShipLog || !cfg.Logging.ShipTrace {
		t.Fatalf("logging overrides = %+v", cfg.Logging)
	}
	if !cfg.Experimental.MGCPProvider || !cfg.Experimental.VAPDProvider || !cfg.Experimental.VABDProvider {
		t.Fatalf("provider overrides = %+v", cfg.Experimental)
	}
	if cfg.OHPCF.Enabled == nil || *cfg.OHPCF.Enabled {
		t.Fatalf("OHPCF override = %v, want false", cfg.OHPCF.Enabled)
	}
}

func TestApplyEnvOverridesRejectsEveryTypedError(t *testing.T) {
	tests := map[string]string{
		"EEBUS_GRPC_BIND":         "",
		"EEBUS_DEBUG_EVENTS":      "invalid",
		"EEBUS_SHIP_LOG":          "invalid",
		"EEBUS_SHIP_TRACE":        "invalid",
		"EEBUS_EXP_VAPD_PROVIDER": "invalid",
		"EEBUS_EXP_VABD_PROVIDER": "invalid",
		"EEBUS_OHPCF_ENABLED":     "invalid",
		"EEBUS_PORT":              "not-a-port",
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, value)
			_, err := applyEnvOverrides(defaultConfig())
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("error = %v, want error naming %s", err, name)
			}
		})
	}
}

func TestValidateCertificatesFromStorage(t *testing.T) {
	autoGenerate := false
	dir := t.TempDir()
	cfg := CertificatesConfig{AutoGenerate: &autoGenerate, StoragePath: dir}
	if err := validateCertificates(cfg); err == nil || !strings.Contains(err.Error(), "existing cert.pem") {
		t.Fatalf("missing storage certificates error = %v", err)
	}
	for _, name := range []string{"cert.pem", "key.pem"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("present"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateCertificates(cfg); err != nil {
		t.Fatalf("existing storage certificates rejected: %v", err)
	}
	if !fileExists(filepath.Join(dir, "cert.pem")) || fileExists(filepath.Join(dir, "absent.pem")) {
		t.Fatal("fileExists returned an unexpected result")
	}
}

func TestValidateGRPCSecurityTLSTokenFiles(t *testing.T) {
	certFile, keyFile := writeConfigTestCertificate(t)
	base := GRPCConfig{Bind: "0.0.0.0", Security: GRPCSecurityConfig{
		Mode: GRPCSecurityModeTLSToken, TLSCertFile: certFile, TLSKeyFile: keyFile,
	}}

	missingToken := base
	missingToken.Security.TokenFile = filepath.Join(t.TempDir(), "missing-token")
	if err := validateGRPCSecurity(missingToken); err == nil || !strings.Contains(err.Error(), "reading gRPC token") {
		t.Fatalf("missing token error = %v", err)
	}

	emptyToken := filepath.Join(t.TempDir(), "empty-token")
	if err := os.WriteFile(emptyToken, []byte(" \n"), 0600); err != nil {
		t.Fatal(err)
	}
	empty := base
	empty.Security.TokenFile = emptyToken
	if err := validateGRPCSecurity(empty); err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("empty token error = %v", err)
	}

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	valid := base
	valid.Security.TokenFile = tokenFile
	if err := validateGRPCSecurity(valid); err != nil {
		t.Fatalf("valid TLS/token config rejected: %v", err)
	}

	invalidPair := valid
	invalidPair.Security.TLSCertFile = tokenFile
	if err := validateGRPCSecurity(invalidPair); err == nil || !strings.Contains(err.Error(), "loading gRPC TLS") {
		t.Fatalf("invalid certificate error = %v", err)
	}
}

func TestValidateSecurityAndProviderModes(t *testing.T) {
	if err := validateGRPCSecurity(GRPCConfig{Bind: "127.0.0.1", Security: GRPCSecurityConfig{Mode: "unknown"}}); err == nil {
		t.Fatal("unknown security mode was accepted")
	}
	for _, bind := range []string{"localhost", "LOCALHOST", "127.0.0.1", "::1"} {
		if err := validateGRPCSecurity(GRPCConfig{Bind: bind, Security: GRPCSecurityConfig{Mode: GRPCSecurityModeLoopback}}); err != nil {
			t.Fatalf("loopback bind %q rejected: %v", bind, err)
		}
	}

	nonLoopback := GRPCConfig{Bind: "0.0.0.0", Security: GRPCSecurityConfig{Mode: GRPCSecurityModeLoopback}}
	if err := validateProviderConfig(ExperimentalConfig{}, nonLoopback); err != nil {
		t.Fatalf("disabled providers rejected: %v", err)
	}
	for _, experimental := range []ExperimentalConfig{
		{MGCPProvider: true}, {VAPDProvider: true}, {VABDProvider: true},
	} {
		if err := validateProviderConfig(experimental, nonLoopback); err == nil {
			t.Fatalf("provider config %+v accepted insecure non-loopback bind", experimental)
		}
	}
	tlsMode := nonLoopback
	tlsMode.Security.Mode = GRPCSecurityModeTLSToken
	if err := validateProviderConfig(ExperimentalConfig{MGCPProvider: true}, tlsMode); err != nil {
		t.Fatalf("TLS provider config rejected: %v", err)
	}
}

func TestCanonicalSKIRejectsInvalidHexAndAcceptsCase(t *testing.T) {
	if !isCanonicalSKI("aAbBcCdDeEfF00112233445566778899AABBCCDD") {
		t.Fatal("mixed-case hexadecimal SKI was rejected")
	}
	if isCanonicalSKI("GABBCCDDEEFF00112233445566778899AABBCCDD") {
		t.Fatal("non-hexadecimal SKI was accepted")
	}
}

func writeConfigTestCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile := filepath.Join(dir, "server.crt")
	keyFile := filepath.Join(dir, "server.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}
