package config_test

import (
	"os"
	"path/filepath"
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
	if cfg.EEBUS.Port != 4712 {
		t.Errorf("EEBUS.Port = %d, want 4712", cfg.EEBUS.Port)
	}
	if cfg.EEBUS.Vendor != "TestVendor" {
		t.Errorf("EEBUS.Vendor = %q, want TestVendor", cfg.EEBUS.Vendor)
	}
	if cfg.EEBUS.Serial != "test-001" {
		t.Errorf("EEBUS.Serial = %q, want test-001", cfg.EEBUS.Serial)
	}
	if !cfg.Certificates.AutoGenerate {
		t.Error("Certificates.AutoGenerate = false, want true")
	}
	if cfg.Certificates.StoragePath != "/tmp/certs" {
		t.Errorf("Certificates.StoragePath = %q, want /tmp/certs", cfg.Certificates.StoragePath)
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
	if !cfg.Certificates.AutoGenerate {
		t.Error("default Certificates.AutoGenerate = false, want true")
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
}
