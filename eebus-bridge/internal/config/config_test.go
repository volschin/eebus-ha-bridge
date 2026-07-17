package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/volschin/eebus-bridge/internal/config"
)

func TestLoadFromFile(t *testing.T) {
	yaml := `
grpc:
  port: 50051
eebus:
  port: 4712
  vendor: "TestVendor"
  brand: "TestBrand"
  model: "TestModel"
  serial: "test-001"
certificates:
  auto_generate: true
  storage_path: "/tmp/certs"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}

	if cfg.GRPC.Port != 50051 {
		t.Errorf("GRPC.Port = %d, want 50051", cfg.GRPC.Port)
	}
	if cfg.GRPC.Security.Mode != config.GRPCSecurityModeLoopback {
		t.Errorf("default gRPC security mode = %q, want loopback", cfg.GRPC.Security.Mode)
	}
	if cfg.EEBUS.Port != 4712 {
		t.Errorf("EEBUS.Port = %d, want 4712", cfg.EEBUS.Port)
	}
	if cfg.EEBUS.Vendor != "TestVendor" {
		t.Errorf("EEBUS.Vendor = %q, want TestVendor", cfg.EEBUS.Vendor)
	}
	if cfg.EEBUS.Serial != "test-001" {
		t.Errorf("EEBUS.Serial = %q, want test-001", cfg.EEBUS.Serial)
	}
	if cfg.Certificates.AutoGenerate == nil || !*cfg.Certificates.AutoGenerate {
		t.Error("Certificates.AutoGenerate = false, want true")
	}
	if cfg.Certificates.StoragePath != "/tmp/certs" {
		t.Errorf("Certificates.StoragePath = %q, want /tmp/certs", cfg.Certificates.StoragePath)
	}
}

func TestRejectsNonLoopbackWithoutTLSToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("grpc:\n  bind: 0.0.0.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadFromFile(path)
	if err == nil {
		t.Fatal("LoadFromFile accepted non-loopback plaintext bind")
	}
	if !strings.Contains(err.Error(), "requires tls_token") {
		t.Fatalf("error = %q, want clear tls_token requirement", err)
	}
}

func TestRejectsIncompleteTLSTokenConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "grpc:\n  bind: 0.0.0.0\n  security:\n    mode: tls_token\n"
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadFromFile(path)
	if err == nil || !strings.Contains(err.Error(), "requires tls_cert_file, tls_key_file, and token_file") {
		t.Fatalf("LoadFromFile error = %v, want missing TLS file error", err)
	}
}

func TestDefaults(t *testing.T) {
	yaml := `{}`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}

	if cfg.GRPC.Port != 50051 {
		t.Errorf("default GRPC.Port = %d, want 50051", cfg.GRPC.Port)
	}
	if cfg.EEBUS.Port != 4712 {
		t.Errorf("default EEBUS.Port = %d, want 4712", cfg.EEBUS.Port)
	}
	if cfg.Certificates.AutoGenerate == nil || !*cfg.Certificates.AutoGenerate {
		t.Error("default Certificates.AutoGenerate = false, want true")
	}
	if cfg.OHPCF.Enabled == nil || !*cfg.OHPCF.Enabled {
		t.Error("default OHPCF.Enabled = false, want true")
	}
}

func TestOHPCFDisable(t *testing.T) {
	yaml := `
ohpcf:
  enabled: false
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}

	if cfg.OHPCF.Enabled == nil || *cfg.OHPCF.Enabled {
		t.Error("OHPCF.Enabled = true, want false")
	}
}

func TestOHPCFEnvOverride(t *testing.T) {
	yaml := `{}`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EEBUS_OHPCF_ENABLED", "false")

	cfg, err := config.LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}

	if cfg.OHPCF.Enabled == nil || *cfg.OHPCF.Enabled {
		t.Error("env override OHPCF.Enabled = true, want false")
	}
}

func TestEnvOverride(t *testing.T) {
	yaml := `
grpc:
  port: 50051
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EEBUS_GRPC_PORT", "9999")
	t.Setenv("EEBUS_SERIAL", "env-serial")
	trustSKI := "AABBCCDDEEFF00112233445566778899AABBCCDD"
	t.Setenv("EEBUS_EXP_TRUST_SKI", trustSKI)

	cfg, err := config.LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}

	if cfg.GRPC.Port != 9999 {
		t.Errorf("env override GRPC.Port = %d, want 9999", cfg.GRPC.Port)
	}
	if cfg.EEBUS.Serial != "env-serial" {
		t.Errorf("env override EEBUS.Serial = %q, want env-serial", cfg.EEBUS.Serial)
	}
	if cfg.Experimental.TrustSKI != trustSKI {
		t.Errorf("env override TrustSKI = %q, want %s", cfg.Experimental.TrustSKI, trustSKI)
	}
}

func TestRejectsUnknownYAMLField(t *testing.T) {
	path := writeConfig(t, "grpc:\n  unexpected: true\n")
	_, err := config.LoadFromFile(path)
	if err == nil || !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("LoadFromFile error = %v, want unknown field error", err)
	}
}

func TestRejectsInvalidEnvironmentOverride(t *testing.T) {
	path := writeConfig(t, "{}")
	for name, value := range map[string]string{
		"EEBUS_GRPC_REFLECTION":    "",
		"EEBUS_EXP_MGCP_PROVIDER":  "definitely",
		"EEBUS_GRPC_SECURITY_MODE": "plaintext",
		"EEBUS_EXP_TRUST_SKI":      "not-a-ski",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, value)
			_, err := config.LoadFromFile(path)
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("LoadFromFile error = %v, want env var name %s", err, name)
			}
		})
	}
}

func TestRejectsInvalidEnvironmentPort(t *testing.T) {
	path := writeConfig(t, "{}")
	for name, value := range map[string]string{
		"EEBUS_GRPC_PORT": "",
		"EEBUS_PORT":      "70000",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, value)
			_, err := config.LoadFromFile(path)
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("LoadFromFile error = %v, want env var name %s", err, name)
			}
		})
	}
}

func TestRejectsInvalidConfiguredPorts(t *testing.T) {
	for name, yaml := range map[string]string{
		"grpc zero":  "grpc:\n  port: 0\n",
		"grpc low":   "grpc:\n  port: -1\n",
		"grpc high":  "grpc:\n  port: 65536\n",
		"eebus zero": "eebus:\n  port: 0\n",
		"eebus low":  "eebus:\n  port: -1\n",
		"eebus high": "eebus:\n  port: 65536\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := config.LoadFromFile(writeConfig(t, yaml))
			if err == nil || !strings.Contains(err.Error(), "1..65535") {
				t.Fatalf("LoadFromFile error = %v, want port range error", err)
			}
		})
	}
}

func TestAcceptsConfiguredPortBoundaries(t *testing.T) {
	for name, yaml := range map[string]string{
		"grpc one":        "grpc:\n  port: 1\n",
		"grpc max":        "grpc:\n  port: 65535\n",
		"eebus one":       "eebus:\n  port: 1\n",
		"eebus max":       "eebus:\n  port: 65535\n",
		"both boundaries": "grpc:\n  port: 1\neebus:\n  port: 65535\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := config.LoadFromFile(writeConfig(t, yaml)); err != nil {
				t.Fatalf("LoadFromFile rejected valid boundary port: %v", err)
			}
		})
	}
}

func TestRejectsContradictoryCertificateOptions(t *testing.T) {
	for name, yaml := range map[string]string{
		"auto and explicit": `
certificates:
  auto_generate: true
  cert_file: /tmp/cert.pem
  key_file: /tmp/key.pem
`,
		"missing key": `
certificates:
  auto_generate: false
  cert_file: /tmp/cert.pem
`,
		"no source": `
certificates:
  auto_generate: false
  storage_path: ""
`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := config.LoadFromFile(writeConfig(t, yaml))
			if err == nil {
				t.Fatal("LoadFromFile accepted contradictory certificate config")
			}
		})
	}
}

func TestAbsentAutoGenerateDefaultsFalseWithExplicitCertificateFiles(t *testing.T) {
	path := writeConfig(t, `
certificates:
  cert_file: /tmp/cert.pem
  key_file: /tmp/key.pem
`)

	cfg, err := config.LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile rejected legacy explicit certificate config: %v", err)
	}
	if cfg.Certificates.AutoGenerate == nil || *cfg.Certificates.AutoGenerate {
		t.Fatalf("AutoGenerate = %v, want explicit false default", cfg.Certificates.AutoGenerate)
	}
}

func TestRejectsInvalidExperimentalTrustSKI(t *testing.T) {
	path := writeConfig(t, "experimental:\n  trust_ski: not-a-ski\n")
	_, err := config.LoadFromFile(path)
	if err == nil || !strings.Contains(err.Error(), "trust_ski") {
		t.Fatalf("LoadFromFile error = %v, want trust_ski validation", err)
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
