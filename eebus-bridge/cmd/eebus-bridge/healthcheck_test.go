package main

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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/volschin/eebus-bridge/internal/config"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
)

func TestRunHealthcheckReportsServingAndNotServing(t *testing.T) {
	server := bridgegrpc.NewServer("127.0.0.1", 0, false)
	port := startHealthcheckServer(t, server)
	configPath := writeHealthcheckConfig(t, port, "")

	if err := runHealthcheck(configPath); err == nil || !strings.Contains(err.Error(), "NOT_SERVING") {
		t.Fatalf("not-serving error = %v", err)
	}
	server.SetHealthy(true)
	if err := runHealthcheck(configPath); err != nil {
		t.Fatalf("serving healthcheck: %v", err)
	}
}

func TestRunHealthcheckReportsConfigurationAndConnectionErrors(t *testing.T) {
	if err := runHealthcheck(filepath.Join(t.TempDir(), "missing.yaml")); err == nil || !strings.Contains(err.Error(), "loading config") {
		t.Fatalf("missing config error = %v", err)
	}
	configPath := writeHealthcheckConfig(t, 1, "")
	if err := runHealthcheck(configPath); err == nil || !strings.Contains(err.Error(), "health check") {
		t.Fatalf("connection error = %v", err)
	}
}

func TestRunHealthcheckWithTLSToken(t *testing.T) {
	certFile, keyFile := writeHealthcheckCertificate(t, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("healthcheck-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	security := config.GRPCSecurityConfig{
		Mode: config.GRPCSecurityModeTLSToken, TLSCertFile: certFile, TLSKeyFile: keyFile, TokenFile: tokenFile,
	}
	server, err := bridgegrpc.NewServerWithSecurity("127.0.0.1", 0, false, security)
	if err != nil {
		t.Fatal(err)
	}
	port := startHealthcheckServer(t, server)
	server.SetHealthy(true)
	configPath := writeHealthcheckConfig(t, port, strings.Join([]string{
		"    mode: tls_token",
		"    tls_cert_file: " + certFile,
		"    tls_key_file: " + keyFile,
		"    token_file: " + tokenFile,
	}, "\n"))
	if err := runHealthcheck(configPath); err != nil {
		t.Fatalf("TLS healthcheck: %v", err)
	}
}

func TestTLSCertificateServerNameValidation(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.pem")
	if _, err := tlsCertificateServerName(missing); err == nil {
		t.Fatal("missing certificate was accepted")
	}

	invalidPEM := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(invalidPEM, []byte("not PEM"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := tlsCertificateServerName(invalidPEM); err == nil || !strings.Contains(err.Error(), "no PEM certificate") {
		t.Fatalf("invalid PEM error = %v", err)
	}

	wrongType := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(wrongType, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("invalid")}), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := tlsCertificateServerName(wrongType); err == nil || !strings.Contains(err.Error(), "no PEM certificate") {
		t.Fatalf("wrong PEM type error = %v", err)
	}

	invalidDER := filepath.Join(t.TempDir(), "invalid-der.pem")
	if err := os.WriteFile(invalidDER, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("invalid")}), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := tlsCertificateServerName(invalidDER); err == nil {
		t.Fatal("invalid certificate DER was accepted")
	}

	dnsCert, _ := writeHealthcheckCertificate(t, []string{"bridge.example"}, nil)
	if got, err := tlsCertificateServerName(dnsCert); err != nil || got != "bridge.example" {
		t.Fatalf("DNS server name = %q, %v", got, err)
	}
	ipCert, _ := writeHealthcheckCertificate(t, nil, []net.IP{net.ParseIP("127.0.0.2")})
	if got, err := tlsCertificateServerName(ipCert); err != nil || got != "127.0.0.2" {
		t.Fatalf("IP server name = %q, %v", got, err)
	}
	noSAN, _ := writeHealthcheckCertificate(t, nil, nil)
	if _, err := tlsCertificateServerName(noSAN); err == nil || !strings.Contains(err.Error(), "no DNS or IP") {
		t.Fatalf("missing SAN error = %v", err)
	}
}

func startHealthcheckServer(t *testing.T, server *bridgegrpc.Server) int {
	t.Helper()
	startErr := make(chan error, 1)
	go func() { startErr <- server.Start() }()
	t.Cleanup(server.Stop)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if address := server.Addr(); address != "" {
			_, portText, err := net.SplitHostPort(address)
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(portText)
			if err != nil {
				t.Fatal(err)
			}
			return port
		}
		select {
		case err := <-startErr:
			t.Fatalf("server start: %v", err)
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server did not start")
	return 0
}

func writeHealthcheckConfig(t *testing.T, port int, securityLines string) string {
	t.Helper()
	content := "grpc:\n  bind: 127.0.0.1\n  port: " + strconv.Itoa(port) + "\n"
	if securityLines != "" {
		content += "  security:\n" + securityLines + "\n"
	}
	content += "certificates:\n  auto_generate: true\n  storage_path: " + t.TempDir() + "\n"
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeHealthcheckCertificate(t *testing.T, dnsNames []string, ipAddresses []net.IP) (string, string) {
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
		DNSNames:    dnsNames, IPAddresses: ipAddresses,
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
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}
