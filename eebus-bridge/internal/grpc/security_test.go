package grpc

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/volschin/eebus-bridge/internal/config"
)

func TestIsLoopbackBind(t *testing.T) {
	cases := []struct {
		bind string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.2", true},
		{"::1", true},
		{"localhost", true},
		{"0.0.0.0", false},
		{"::", false},
		{"", false},
		{"192.168.68.119", false},
		{"10.0.0.5", false},
		{"example.com", false},
	}
	for _, tc := range cases {
		if got := isLoopbackBind(tc.bind); got != tc.want {
			t.Errorf("isLoopbackBind(%q) = %v, want %v", tc.bind, got, tc.want)
		}
	}
}

func TestSecuritySetupDoesNotLogSecretMaterial(t *testing.T) {
	const token = "log-redaction-token-d1f168"
	certFile, keyFile, tokenFile := writeTestCredentials(t, token)
	keyMaterial, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)

	srv, err := NewServerWithSecurity("127.0.0.1", 0, false, config.GRPCSecurityConfig{
		Mode: config.GRPCSecurityModeTLSToken, TLSCertFile: certFile, TLSKeyFile: keyFile, TokenFile: tokenFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.Stop()
	if strings.Contains(logs.String(), token) || strings.Contains(logs.String(), string(keyMaterial)) {
		t.Fatalf("security setup log exposed token or private key material: %q", logs.String())
	}
}

func TestRegisterPushServicesGatedOnLoopback(t *testing.T) {
	grid := NewGridService(nil)
	viz := NewVisualizationService(nil, nil)

	t.Run("loopback registers", func(t *testing.T) {
		srv := NewServer("127.0.0.1", 0, false)
		if !RegisterPushServices(srv, "127.0.0.1", config.GRPCSecurityModeLoopback, grid, viz) {
			t.Fatal("expected push services to register on loopback bind")
		}
	})

	t.Run("exposed bind refused", func(t *testing.T) {
		srv := NewServer("127.0.0.1", 0, false)
		if RegisterPushServices(srv, "0.0.0.0", config.GRPCSecurityModeLoopback, grid, viz) {
			t.Fatal("expected push services to be refused on routable bind")
		}
	})

	t.Run("secured remote registers", func(t *testing.T) {
		srv := NewServer("127.0.0.1", 0, false)
		if !RegisterPushServices(srv, "0.0.0.0", config.GRPCSecurityModeTLSToken, grid, viz) {
			t.Fatal("expected push services to register with tls_token")
		}
	})
}
