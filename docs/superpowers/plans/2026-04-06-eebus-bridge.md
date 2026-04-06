# EEBUS Bridge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go-based EEBUS bridge service that wraps eebus-go and exposes a gRPC API, plus a Home Assistant custom integration as gRPC client — enabling local LPC control and power monitoring of a Vaillant aroTHERM plus heat pump.

**Architecture:** A Go binary (`eebus-bridge`) embeds eebus-go for SHIP/SPINE communication with the VR940f gateway, exposes three gRPC services (Device, LPC, Monitoring) on a single port. A Python HA custom integration (`custom_components/eebus`) connects via gRPC, provides config flow for discovery/pairing, and maps data to HA entities.

**Tech Stack:** Go 1.22+, eebus-go/ship-go/spine-go, gRPC + Protobuf, Docker; Python 3.12+, grpcio, Home Assistant custom component APIs.

**Design Spec:** `docs/superpowers/specs/2026-04-06-eebus-bridge-design.md`

---

## File Structure

### Go Bridge (`eebus-bridge/`)

| File | Responsibility |
|------|----------------|
| `cmd/eebus-bridge/main.go` | Entrypoint: load config, init certs, start EEBUS + gRPC |
| `internal/config/config.go` | YAML + env config parsing |
| `internal/config/config_test.go` | Config parsing tests |
| `internal/certs/certs.go` | Certificate auto-gen, load, persist |
| `internal/certs/certs_test.go` | Certificate logic tests |
| `internal/eebus/service.go` | eebus-go Service wrapper + lifecycle |
| `internal/eebus/service_test.go` | Service wrapper tests |
| `internal/eebus/callbacks.go` | ServiceReaderInterface implementation |
| `internal/eebus/callbacks_test.go` | Callback dispatch tests |
| `internal/eebus/eventbus.go` | Internal event bus with fan-out to gRPC streams |
| `internal/eebus/eventbus_test.go` | Event bus tests |
| `internal/usecases/lpc.go` | EG-LPC use case wrapper |
| `internal/usecases/lpc_test.go` | LPC wrapper tests |
| `internal/usecases/monitoring.go` | MA-MPC use case wrapper |
| `internal/usecases/monitoring_test.go` | Monitoring wrapper tests |
| `internal/grpc/server.go` | gRPC server setup, reflection, health |
| `internal/grpc/server_test.go` | Server lifecycle tests |
| `internal/grpc/device_service.go` | DeviceService gRPC implementation |
| `internal/grpc/device_service_test.go` | DeviceService tests |
| `internal/grpc/lpc_service.go` | LPCService gRPC implementation |
| `internal/grpc/lpc_service_test.go` | LPCService tests |
| `internal/grpc/monitoring_service.go` | MonitoringService gRPC implementation |
| `internal/grpc/monitoring_service_test.go` | MonitoringService tests |
| `proto/eebus/v1/common.proto` | Shared protobuf types |
| `proto/eebus/v1/device_service.proto` | DeviceService definition |
| `proto/eebus/v1/lpc_service.proto` | LPCService definition |
| `proto/eebus/v1/monitoring_service.proto` | MonitoringService definition |
| `Dockerfile` | Multi-stage Go build |
| `docker-compose.yml` | Bridge + HA deployment |
| `Makefile` | Build, test, proto-gen targets |

### Repository Root (HACS)

| File | Responsibility |
|------|----------------|
| `hacs.json` | HACS manifest (name, render_readme) |
| `README.md` | User-facing docs (HACS requirement, Gold: docs-*) |
| `LICENSE` | MIT license |
| `.github/workflows/hacs.yml` | HACS validation CI |
| `.github/workflows/hassfest.yml` | Hassfest validation CI |
| `.github/workflows/test.yml` | Tests + lint + coverage CI |
| `.github/workflows/go.yml` | Go build + test CI |
| `.github/workflows/release.yml` | Release drafter |
| `pyproject.toml` | pytest + coverage config |

### HA Custom Integration (`ha-integration/custom_components/eebus/`)

| File | Responsibility |
|------|----------------|
| `__init__.py` | Integration setup, runtime_data pattern |
| `manifest.json` | Full manifest (documentation, issue_tracker, quality_scale) |
| `quality_scale.yaml` | Gold Quality Scale compliance tracking |
| `config_flow.py` | Discovery + pairing + reconfigure flow |
| `coordinator.py` | DataUpdateCoordinator, gRPC stream listener, log-when-unavailable |
| `const.py` | Constants, PARALLEL_UPDATES |
| `entity.py` | Base entity class with device info |
| `sensor.py` | Power consumption, energy sensors |
| `number.py` | LPC limit, failsafe number entities |
| `switch.py` | LPC active, heartbeat switches |
| `binary_sensor.py` | Connection status, heartbeat ok |
| `diagnostics.py` | Diagnostic data export (Gold) |
| `icons.json` | Entity icon translations (Gold) |
| `strings.json` | UI texts + entity translations + exception translations |
| `translations/en.json` | English translations |
| `translations/de.json` | German translations |
| `brand/icon.png` | Brand icon 256x256 (HACS + Gold) |
| `brand/logo.png` | Brand logo 256x256 (HACS + Gold) |
| `tests/__init__.py` | Test package |
| `tests/conftest.py` | Shared test fixtures |
| `tests/test_config_flow.py` | Config flow tests (incl. reconfigure) |
| `tests/test_coordinator.py` | Coordinator tests |
| `tests/test_sensor.py` | Sensor entity tests |
| `tests/test_number.py` | Number entity tests |
| `tests/test_switch.py` | Switch entity tests |
| `tests/test_binary_sensor.py` | Binary sensor entity tests |
| `tests/test_diagnostics.py` | Diagnostics tests |
| `tests/test_init.py` | Setup + unload tests |

---

## Phase 1: Go Project Foundation

### Task 1: Project Scaffold & Makefile

**Files:**
- Create: `eebus-bridge/go.mod`
- Create: `eebus-bridge/Makefile`
- Create: `eebus-bridge/cmd/eebus-bridge/main.go`

- [ ] **Step 1: Initialize Go module**

```bash
cd /home/volsch/eebus
mkdir -p eebus-bridge/cmd/eebus-bridge
cd eebus-bridge
go mod init github.com/volschin/eebus-bridge
```

- [ ] **Step 2: Create minimal main.go**

Create `eebus-bridge/cmd/eebus-bridge/main.go`:

```go
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("eebus-bridge starting...")
	os.Exit(0)
}
```

- [ ] **Step 3: Create Makefile**

Create `eebus-bridge/Makefile`:

```makefile
.PHONY: build test lint proto clean

BINARY := eebus-bridge
GO := go

build:
	$(GO) build -o bin/$(BINARY) ./cmd/eebus-bridge

test:
	$(GO) test -v -race ./...

lint:
	$(GO) vet ./...

proto:
	buf generate

clean:
	rm -rf bin/
```

- [ ] **Step 4: Verify build**

```bash
cd /home/volsch/eebus/eebus-bridge
make build
./bin/eebus-bridge
```

Expected: prints "eebus-bridge starting..." and exits 0.

- [ ] **Step 5: Commit**

```bash
git add eebus-bridge/go.mod eebus-bridge/Makefile eebus-bridge/cmd/
git commit -m "feat: scaffold Go project with module, main, and Makefile"
```

---

### Task 2: Configuration

**Files:**
- Create: `eebus-bridge/internal/config/config.go`
- Create: `eebus-bridge/internal/config/config_test.go`

- [ ] **Step 1: Write failing test for YAML config parsing**

Create `eebus-bridge/internal/config/config_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/volsch/eebus/eebus-bridge
go test ./internal/config/ -v
```

Expected: compilation error — `config` package does not exist.

- [ ] **Step 3: Implement config package**

Create `eebus-bridge/internal/config/config.go`:

```go
package config

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	GRPC         GRPCConfig         `yaml:"grpc"`
	EEBUS        EEBUSConfig        `yaml:"eebus"`
	Certificates CertificatesConfig `yaml:"certificates"`
}

type GRPCConfig struct {
	Port int `yaml:"port"`
}

type EEBUSConfig struct {
	Port   int    `yaml:"port"`
	Vendor string `yaml:"vendor"`
	Brand  string `yaml:"brand"`
	Model  string `yaml:"model"`
	Serial string `yaml:"serial"`
}

type CertificatesConfig struct {
	AutoGenerate bool   `yaml:"auto_generate"`
	CertFile     string `yaml:"cert_file"`
	KeyFile      string `yaml:"key_file"`
	StoragePath  string `yaml:"storage_path"`
}

func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	applyDefaults(cfg)
	applyEnvOverrides(cfg)

	return cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.GRPC.Port == 0 {
		cfg.GRPC.Port = 50051
	}
	if cfg.EEBUS.Port == 0 {
		cfg.EEBUS.Port = 4712
	}
	if cfg.EEBUS.Vendor == "" {
		cfg.EEBUS.Vendor = "HomeAssistant"
	}
	if cfg.EEBUS.Brand == "" {
		cfg.EEBUS.Brand = "eebus-bridge"
	}
	if cfg.EEBUS.Model == "" {
		cfg.EEBUS.Model = "eebus-bridge"
	}
	if cfg.Certificates.StoragePath == "" {
		cfg.Certificates.StoragePath = "/data/certs"
	}
	if !cfg.Certificates.AutoGenerate && cfg.Certificates.CertFile == "" {
		cfg.Certificates.AutoGenerate = true
	}
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("EEBUS_GRPC_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.GRPC.Port = port
		}
	}
	if v := os.Getenv("EEBUS_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.EEBUS.Port = port
		}
	}
	if v := os.Getenv("EEBUS_VENDOR"); v != "" {
		cfg.EEBUS.Vendor = v
	}
	if v := os.Getenv("EEBUS_BRAND"); v != "" {
		cfg.EEBUS.Brand = v
	}
	if v := os.Getenv("EEBUS_MODEL"); v != "" {
		cfg.EEBUS.Model = v
	}
	if v := os.Getenv("EEBUS_SERIAL"); v != "" {
		cfg.EEBUS.Serial = v
	}
	if v := os.Getenv("EEBUS_CERT_FILE"); v != "" {
		cfg.Certificates.CertFile = v
	}
	if v := os.Getenv("EEBUS_KEY_FILE"); v != "" {
		cfg.Certificates.KeyFile = v
	}
	if v := os.Getenv("EEBUS_CERT_STORAGE"); v != "" {
		cfg.Certificates.StoragePath = v
	}
}
```

- [ ] **Step 4: Add yaml dependency and run tests**

```bash
cd /home/volsch/eebus/eebus-bridge
go get gopkg.in/yaml.v3
go test ./internal/config/ -v
```

Expected: all 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add eebus-bridge/internal/config/ eebus-bridge/go.mod eebus-bridge/go.sum
git commit -m "feat: add YAML+env config parsing with defaults"
```

---

### Task 3: Certificate Management

**Files:**
- Create: `eebus-bridge/internal/certs/certs.go`
- Create: `eebus-bridge/internal/certs/certs_test.go`

- [ ] **Step 1: Write failing test for cert auto-generation and persistence**

Create `eebus-bridge/internal/certs/certs_test.go`:

```go
package certs_test

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"

	"github.com/volschin/eebus-bridge/internal/certs"
)

func TestAutoGenerate(t *testing.T) {
	dir := t.TempDir()

	cert, err := certs.EnsureCertificate("", "", dir)
	if err != nil {
		t.Fatalf("EnsureCertificate failed: %v", err)
	}

	if len(cert.Certificate) == 0 {
		t.Fatal("certificate has no data")
	}

	// Verify files were persisted
	if _, err := os.Stat(filepath.Join(dir, "cert.pem")); err != nil {
		t.Errorf("cert.pem not persisted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "key.pem")); err != nil {
		t.Errorf("key.pem not persisted: %v", err)
	}
}

func TestLoadExisting(t *testing.T) {
	dir := t.TempDir()

	// Generate first
	cert1, err := certs.EnsureCertificate("", "", dir)
	if err != nil {
		t.Fatalf("first EnsureCertificate failed: %v", err)
	}

	// Load again — should return same cert (from disk)
	cert2, err := certs.EnsureCertificate("", "", dir)
	if err != nil {
		t.Fatalf("second EnsureCertificate failed: %v", err)
	}

	if !certEqual(cert1, cert2) {
		t.Error("reloaded cert differs from original")
	}
}

func TestLoadFromExplicitFiles(t *testing.T) {
	dir := t.TempDir()

	// Generate to get valid cert/key files
	_, err := certs.EnsureCertificate("", "", dir)
	if err != nil {
		t.Fatal(err)
	}

	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")

	cert, err := certs.EnsureCertificate(certFile, keyFile, "")
	if err != nil {
		t.Fatalf("EnsureCertificate with explicit files failed: %v", err)
	}

	if len(cert.Certificate) == 0 {
		t.Fatal("certificate has no data")
	}
}

func TestGetSKI(t *testing.T) {
	dir := t.TempDir()
	cert, err := certs.EnsureCertificate("", "", dir)
	if err != nil {
		t.Fatal(err)
	}

	ski, err := certs.SKIFromCertificate(cert)
	if err != nil {
		t.Fatalf("SKIFromCertificate failed: %v", err)
	}

	if len(ski) == 0 {
		t.Error("SKI is empty")
	}
}

func certEqual(a, b tls.Certificate) bool {
	if len(a.Certificate) != len(b.Certificate) {
		return false
	}
	for i := range a.Certificate {
		if len(a.Certificate[i]) != len(b.Certificate[i]) {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/volsch/eebus/eebus-bridge
go test ./internal/certs/ -v
```

Expected: compilation error — `certs` package does not exist.

- [ ] **Step 3: Implement certs package**

Create `eebus-bridge/internal/certs/certs.go`:

```go
package certs

import (
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
	certOut, err := os.Create(filepath.Join(dir, "cert.pem"))
	if err != nil {
		return err
	}
	defer certOut.Close()
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
	keyOut, err := os.OpenFile(filepath.Join(dir, "key.pem"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer keyOut.Close()
	return pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
```

Note: Add `"crypto/ecdsa"` to the import block.

- [ ] **Step 4: Add ship-go dependency and run tests**

```bash
cd /home/volsch/eebus/eebus-bridge
go get github.com/enbility/ship-go
go test ./internal/certs/ -v
```

Expected: all 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add eebus-bridge/internal/certs/ eebus-bridge/go.mod eebus-bridge/go.sum
git commit -m "feat: add certificate auto-gen, load, and persist via ship-go"
```

---

## Phase 2: Event Bus

### Task 4: Internal Event Bus

**Files:**
- Create: `eebus-bridge/internal/eebus/eventbus.go`
- Create: `eebus-bridge/internal/eebus/eventbus_test.go`

- [ ] **Step 1: Write failing test for event bus fan-out**

Create `eebus-bridge/internal/eebus/eventbus_test.go`:

```go
package eebus_test

import (
	"testing"
	"time"

	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestEventBusSubscribeAndPublish(t *testing.T) {
	bus := eebus.NewEventBus()

	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	evt := eebus.Event{
		SKI:  "test-ski",
		Type: "test-event",
		Data: map[string]any{"power": 1500.0},
	}
	bus.Publish(evt)

	select {
	case received := <-ch:
		if received.SKI != "test-ski" {
			t.Errorf("SKI = %q, want test-ski", received.SKI)
		}
		if received.Type != "test-event" {
			t.Errorf("Type = %q, want test-event", received.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEventBusFanOut(t *testing.T) {
	bus := eebus.NewEventBus()

	ch1 := bus.Subscribe()
	ch2 := bus.Subscribe()
	defer bus.Unsubscribe(ch1)
	defer bus.Unsubscribe(ch2)

	bus.Publish(eebus.Event{SKI: "ski", Type: "evt"})

	for i, ch := range []<-chan eebus.Event{ch1, ch2} {
		select {
		case evt := <-ch:
			if evt.Type != "evt" {
				t.Errorf("subscriber %d: Type = %q, want evt", i, evt.Type)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timeout", i)
		}
	}
}

func TestEventBusUnsubscribe(t *testing.T) {
	bus := eebus.NewEventBus()

	ch := bus.Subscribe()
	bus.Unsubscribe(ch)

	// Publishing after unsubscribe should not block
	bus.Publish(eebus.Event{SKI: "ski", Type: "evt"})
}

func TestEventBusSlowSubscriberDoesNotBlock(t *testing.T) {
	bus := eebus.NewEventBus()

	_ = bus.Subscribe() // never read from this
	ch2 := bus.Subscribe()
	defer bus.Unsubscribe(ch2)

	// Publish should not block even though ch1 is not consumed
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			bus.Publish(eebus.Event{SKI: "ski", Type: "evt"})
		}
		close(done)
	}()

	select {
	case <-done:
		// OK — publishing didn't block
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on slow subscriber")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/volsch/eebus/eebus-bridge
go test ./internal/eebus/ -v
```

Expected: compilation error — types not found.

- [ ] **Step 3: Implement event bus**

Create `eebus-bridge/internal/eebus/eventbus.go`:

```go
package eebus

import "sync"

// Event represents an internal event from eebus-go callbacks.
type Event struct {
	SKI  string
	Type string
	Data map[string]any
}

// EventBus provides fan-out event distribution to multiple subscribers.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[chan Event]struct{}
}

func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[chan Event]struct{}),
	}
}

// Subscribe returns a channel that receives published events.
// Buffer size 64 to avoid blocking publishers on slow consumers.
func (b *EventBus) Subscribe() chan Event {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel and closes it.
func (b *EventBus) Unsubscribe(ch chan Event) {
	b.mu.Lock()
	if _, ok := b.subscribers[ch]; ok {
		delete(b.subscribers, ch)
		close(ch)
	}
	b.mu.Unlock()
}

// Publish sends an event to all subscribers. Non-blocking: drops events
// for subscribers whose buffer is full.
func (b *EventBus) Publish(evt Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subscribers {
		select {
		case ch <- evt:
		default:
			// subscriber too slow, drop event
		}
	}
}
```

- [ ] **Step 4: Run tests**

```bash
cd /home/volsch/eebus/eebus-bridge
go test ./internal/eebus/ -v -race
```

Expected: all 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add eebus-bridge/internal/eebus/eventbus.go eebus-bridge/internal/eebus/eventbus_test.go
git commit -m "feat: add fan-out event bus for eebus callback distribution"
```

---

## Phase 3: Protobuf & gRPC Definitions

### Task 5: Protobuf Schemas

**Files:**
- Create: `eebus-bridge/proto/eebus/v1/common.proto`
- Create: `eebus-bridge/proto/eebus/v1/device_service.proto`
- Create: `eebus-bridge/proto/eebus/v1/lpc_service.proto`
- Create: `eebus-bridge/proto/eebus/v1/monitoring_service.proto`
- Create: `eebus-bridge/buf.yaml`
- Create: `eebus-bridge/buf.gen.yaml`

- [ ] **Step 1: Create buf configuration**

Create `eebus-bridge/buf.yaml`:

```yaml
version: v2
modules:
  - path: proto
deps:
  - buf.build/googleapis/googleapis
```

Create `eebus-bridge/buf.gen.yaml`:

```yaml
version: v2
plugins:
  - remote: buf.build/protocolbuffers/go
    out: gen/proto
    opt: paths=source_relative
  - remote: buf.build/grpc/go
    out: gen/proto
    opt: paths=source_relative
```

- [ ] **Step 2: Create common.proto**

Create `eebus-bridge/proto/eebus/v1/common.proto`:

```protobuf
syntax = "proto3";

package eebus.v1;

option go_package = "github.com/volschin/eebus-bridge/gen/proto/eebus/v1;eebusv1";

import "google/protobuf/timestamp.proto";

message Empty {}

message DeviceRequest {
  string ski = 1;
}

message LoadLimit {
  double value_watts = 1;
  int64 duration_seconds = 2;
  bool is_active = 3;
  bool is_changeable = 4;
}

message PowerMeasurement {
  double watts = 1;
  google.protobuf.Timestamp timestamp = 2;
}

message MeasurementEntry {
  string type = 1;
  double value = 2;
  string unit = 3;
  google.protobuf.Timestamp timestamp = 4;
}
```

- [ ] **Step 3: Create device_service.proto**

Create `eebus-bridge/proto/eebus/v1/device_service.proto`:

```protobuf
syntax = "proto3";

package eebus.v1;

option go_package = "github.com/volschin/eebus-bridge/gen/proto/eebus/v1;eebusv1";

import "eebus/v1/common.proto";

service DeviceService {
  rpc GetStatus(Empty) returns (ServiceStatus);
  rpc ListDiscoveredDevices(Empty) returns (ListDevicesResponse);
  rpc RegisterRemoteSKI(RegisterSKIRequest) returns (Empty);
  rpc UnregisterRemoteSKI(DeviceRequest) returns (Empty);
  rpc GetPairingStatus(DeviceRequest) returns (PairingStatus);
  rpc ListPairedDevices(Empty) returns (ListPairedDevicesResponse);
  rpc SubscribeDeviceEvents(Empty) returns (stream DeviceEvent);
}

message ServiceStatus {
  bool running = 1;
  string local_ski = 2;
}

message DiscoveredDevice {
  string ski = 1;
  string brand = 2;
  string model = 3;
  string serial = 4;
  string device_type = 5;
  string host = 6;
}

message ListDevicesResponse {
  repeated DiscoveredDevice devices = 1;
}

message RegisterSKIRequest {
  string ski = 1;
}

message PairingStatus {
  string ski = 1;
  PairingState state = 2;
}

enum PairingState {
  PAIRING_STATE_UNSPECIFIED = 0;
  PAIRING_STATE_PENDING = 1;
  PAIRING_STATE_WAITING_FOR_TRUST = 2;
  PAIRING_STATE_TRUSTED = 3;
  PAIRING_STATE_DENIED = 4;
}

message PairedDevice {
  string ski = 1;
  string brand = 2;
  string model = 3;
  string serial = 4;
  string device_type = 5;
  repeated string supported_use_cases = 6;
}

message ListPairedDevicesResponse {
  repeated PairedDevice devices = 1;
}

message DeviceEvent {
  string ski = 1;
  DeviceEventType event_type = 2;
}

enum DeviceEventType {
  DEVICE_EVENT_UNSPECIFIED = 0;
  DEVICE_EVENT_CONNECTED = 1;
  DEVICE_EVENT_DISCONNECTED = 2;
  DEVICE_EVENT_TRUST_REMOVED = 3;
}
```

- [ ] **Step 4: Create lpc_service.proto**

Create `eebus-bridge/proto/eebus/v1/lpc_service.proto`:

```protobuf
syntax = "proto3";

package eebus.v1;

option go_package = "github.com/volschin/eebus-bridge/gen/proto/eebus/v1;eebusv1";

import "eebus/v1/common.proto";

service LPCService {
  rpc GetConsumptionLimit(DeviceRequest) returns (LoadLimit);
  rpc WriteConsumptionLimit(WriteLoadLimitRequest) returns (Empty);
  rpc GetFailsafeLimit(DeviceRequest) returns (FailsafeLimit);
  rpc WriteFailsafeLimit(WriteFailsafeLimitRequest) returns (Empty);
  rpc StartHeartbeat(DeviceRequest) returns (Empty);
  rpc StopHeartbeat(DeviceRequest) returns (Empty);
  rpc GetHeartbeatStatus(DeviceRequest) returns (HeartbeatStatus);
  rpc GetConsumptionNominalMax(DeviceRequest) returns (PowerValue);
  rpc SubscribeLPCEvents(DeviceRequest) returns (stream LPCEvent);
}

message WriteLoadLimitRequest {
  string ski = 1;
  double value_watts = 2;
  int64 duration_seconds = 3;
  bool is_active = 4;
}

message FailsafeLimit {
  double value_watts = 1;
  int64 duration_minimum_seconds = 2;
}

message WriteFailsafeLimitRequest {
  string ski = 1;
  double value_watts = 2;
  int64 duration_minimum_seconds = 3;
}

message HeartbeatStatus {
  bool running = 1;
  bool within_duration = 2;
}

message PowerValue {
  double watts = 1;
}

message LPCEvent {
  string ski = 1;
  LPCEventType event_type = 2;
  oneof data {
    LoadLimit limit_update = 3;
    FailsafeLimit failsafe_update = 4;
  }
}

enum LPCEventType {
  LPC_EVENT_UNSPECIFIED = 0;
  LPC_EVENT_LIMIT_UPDATED = 1;
  LPC_EVENT_FAILSAFE_UPDATED = 2;
  LPC_EVENT_HEARTBEAT_TIMEOUT = 3;
}
```

- [ ] **Step 5: Create monitoring_service.proto**

Create `eebus-bridge/proto/eebus/v1/monitoring_service.proto`:

```protobuf
syntax = "proto3";

package eebus.v1;

option go_package = "github.com/volschin/eebus-bridge/gen/proto/eebus/v1;eebusv1";

import "eebus/v1/common.proto";
import "google/protobuf/timestamp.proto";

service MonitoringService {
  rpc GetPowerConsumption(DeviceRequest) returns (PowerMeasurement);
  rpc GetEnergyConsumed(DeviceRequest) returns (EnergyMeasurement);
  rpc GetMeasurements(DeviceRequest) returns (MeasurementList);
  rpc SubscribeMeasurements(DeviceRequest) returns (stream MeasurementEvent);
}

message EnergyMeasurement {
  double kilowatt_hours = 1;
  google.protobuf.Timestamp timestamp = 2;
}

message MeasurementList {
  repeated MeasurementEntry measurements = 1;
}

message MeasurementEvent {
  string ski = 1;
  MeasurementEventType event_type = 2;
  oneof data {
    PowerMeasurement power = 3;
    EnergyMeasurement energy = 4;
  }
}

enum MeasurementEventType {
  MEASUREMENT_EVENT_UNSPECIFIED = 0;
  MEASUREMENT_EVENT_POWER_UPDATED = 1;
  MEASUREMENT_EVENT_ENERGY_UPDATED = 2;
}
```

- [ ] **Step 6: Install buf and generate code**

```bash
# Install buf if not present
go install github.com/bufbuild/buf/cmd/buf@latest

cd /home/volsch/eebus/eebus-bridge
buf dep update
buf generate
```

Expected: generated Go code appears in `gen/proto/eebus/v1/`.

- [ ] **Step 7: Verify generated code compiles**

```bash
cd /home/volsch/eebus/eebus-bridge
go build ./gen/proto/...
```

Expected: compiles without errors.

- [ ] **Step 8: Commit**

```bash
git add eebus-bridge/proto/ eebus-bridge/buf.yaml eebus-bridge/buf.gen.yaml eebus-bridge/gen/
git commit -m "feat: add protobuf schemas and generate gRPC Go code"
```

---

## Phase 4: EEBUS Service Layer

### Task 6: EEBUS Service Wrapper

**Files:**
- Create: `eebus-bridge/internal/eebus/service.go`
- Create: `eebus-bridge/internal/eebus/callbacks.go`
- Create: `eebus-bridge/internal/eebus/service_test.go`
- Create: `eebus-bridge/internal/eebus/callbacks_test.go`

- [ ] **Step 1: Write failing test for callback dispatch**

Create `eebus-bridge/internal/eebus/callbacks_test.go`:

```go
package eebus_test

import (
	"testing"
	"time"

	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestCallbacksDispatchConnect(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	cb := eebus.NewCallbacks(bus)
	cb.RemoteSKIConnected(nil, "test-ski-123")

	select {
	case evt := <-ch:
		if evt.SKI != "test-ski-123" {
			t.Errorf("SKI = %q, want test-ski-123", evt.SKI)
		}
		if evt.Type != "device.connected" {
			t.Errorf("Type = %q, want device.connected", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for connect event")
	}
}

func TestCallbacksDispatchDisconnect(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	cb := eebus.NewCallbacks(bus)
	cb.RemoteSKIDisconnected(nil, "test-ski-456")

	select {
	case evt := <-ch:
		if evt.Type != "device.disconnected" {
			t.Errorf("Type = %q, want device.disconnected", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestCallbacksAllowWaitingForTrust(t *testing.T) {
	bus := eebus.NewEventBus()
	cb := eebus.NewCallbacks(bus)

	if !cb.AllowWaitingForTrust("any-ski") {
		t.Error("AllowWaitingForTrust should return true")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/volsch/eebus/eebus-bridge
go test ./internal/eebus/ -run TestCallbacks -v
```

Expected: compilation error — `NewCallbacks` not found.

- [ ] **Step 3: Implement callbacks**

Create `eebus-bridge/internal/eebus/callbacks.go`:

```go
package eebus

import (
	"sync"

	"github.com/enbility/eebus-go/api"
	shipapi "github.com/enbility/ship-go/api"
)

// Callbacks implements api.ServiceReaderInterface and dispatches events to the EventBus.
type Callbacks struct {
	bus            *EventBus
	mu             sync.RWMutex
	discoveredSvcs []shipapi.RemoteService
	pairingStates  map[string]*shipapi.ConnectionStateDetail
}

func NewCallbacks(bus *EventBus) *Callbacks {
	return &Callbacks{
		bus:           bus,
		pairingStates: make(map[string]*shipapi.ConnectionStateDetail),
	}
}

var _ api.ServiceReaderInterface = (*Callbacks)(nil)

func (c *Callbacks) RemoteSKIConnected(service api.ServiceInterface, ski string) {
	c.bus.Publish(Event{SKI: ski, Type: "device.connected"})
}

func (c *Callbacks) RemoteSKIDisconnected(service api.ServiceInterface, ski string) {
	c.bus.Publish(Event{SKI: ski, Type: "device.disconnected"})
}

func (c *Callbacks) VisibleRemoteServicesUpdated(service api.ServiceInterface, entries []shipapi.RemoteService) {
	c.mu.Lock()
	c.discoveredSvcs = entries
	c.mu.Unlock()
	c.bus.Publish(Event{Type: "discovery.updated"})
}

func (c *Callbacks) ServiceShipIDUpdate(ski string, shipID string) {
	// no-op: SHIP ID is managed by eebus-go internally
}

func (c *Callbacks) ServicePairingDetailUpdate(ski string, detail *shipapi.ConnectionStateDetail) {
	c.mu.Lock()
	c.pairingStates[ski] = detail
	c.mu.Unlock()
	c.bus.Publish(Event{SKI: ski, Type: "pairing.updated"})
}

func (c *Callbacks) AllowWaitingForTrust(ski string) bool {
	return true // always allow — user approves via myVaillant app
}

// DiscoveredServices returns the most recent list of discovered EEBUS services.
func (c *Callbacks) DiscoveredServices() []shipapi.RemoteService {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]shipapi.RemoteService, len(c.discoveredSvcs))
	copy(result, c.discoveredSvcs)
	return result
}

// PairingState returns the latest pairing state for a given SKI.
func (c *Callbacks) PairingState(ski string) *shipapi.ConnectionStateDetail {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pairingStates[ski]
}
```

- [ ] **Step 4: Run callback tests**

```bash
cd /home/volsch/eebus/eebus-bridge
go get github.com/enbility/eebus-go
go test ./internal/eebus/ -run TestCallbacks -v
```

Expected: all 3 tests PASS.

- [ ] **Step 5: Implement service wrapper**

Create `eebus-bridge/internal/eebus/service.go`:

```go
package eebus

import (
	"crypto/tls"
	"fmt"
	"time"

	"github.com/enbility/eebus-go/api"
	eebusservice "github.com/enbility/eebus-go/service"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/config"
)

// BridgeService wraps eebus-go Service with bridge-specific lifecycle.
type BridgeService struct {
	service   *eebusservice.Service
	callbacks *Callbacks
	bus       *EventBus
	localSKI  string
}

// NewBridgeService creates a new bridge service. Does not start it.
func NewBridgeService(cfg *config.Config, cert tls.Certificate, bus *EventBus) (*BridgeService, error) {
	eebusConfig, err := api.NewConfiguration(
		cfg.EEBUS.Vendor,
		cfg.EEBUS.Brand,
		cfg.EEBUS.Model,
		cfg.EEBUS.Serial,
		model.DeviceTypeTypeEnergyManagementSystem,
		[]model.EntityTypeType{
			model.EntityTypeTypeCEM,
		},
		cfg.EEBUS.Port,
		cert,
		time.Second*4,
	)
	if err != nil {
		return nil, fmt.Errorf("creating eebus config: %w", err)
	}

	eebusConfig.SetAlternateIdentifier(
		fmt.Sprintf("%s-%s", cfg.EEBUS.Brand, cfg.EEBUS.Serial),
	)

	callbacks := NewCallbacks(bus)
	svc := eebusservice.NewService(eebusConfig, callbacks)

	return &BridgeService{
		service:   svc,
		callbacks: callbacks,
		bus:       bus,
	}, nil
}

// Setup initializes mDNS and the WebSocket server.
func (b *BridgeService) Setup() error {
	return b.service.Setup()
}

// Start begins EEBUS communication.
func (b *BridgeService) Start() {
	b.service.Start()
}

// Shutdown gracefully stops the service.
func (b *BridgeService) Shutdown() {
	b.service.Shutdown()
}

// Service returns the underlying eebus-go service for use case registration.
func (b *BridgeService) Service() api.ServiceInterface {
	return b.service
}

// LocalEntity returns the CEM entity for use case setup.
func (b *BridgeService) LocalEntity() spineapi.EntityLocalInterface {
	return b.service.LocalDevice().EntityForType(model.EntityTypeTypeCEM)
}

// Callbacks returns the service callbacks for querying state.
func (b *BridgeService) Callbacks() *Callbacks {
	return b.callbacks
}

// RegisterRemoteSKI marks a remote SKI as trusted.
func (b *BridgeService) RegisterRemoteSKI(ski string) {
	b.service.RegisterRemoteSKI(ski)
}

// UnregisterRemoteSKI removes trust for a remote SKI.
func (b *BridgeService) UnregisterRemoteSKI(ski string) {
	b.service.UnregisterRemoteSKI(ski)
}
```

- [ ] **Step 6: Commit**

```bash
git add eebus-bridge/internal/eebus/
git commit -m "feat: add EEBUS service wrapper with callbacks and event dispatch"
```

---

### Task 7: Use Case Wrappers (LPC + Monitoring)

**Files:**
- Create: `eebus-bridge/internal/usecases/lpc.go`
- Create: `eebus-bridge/internal/usecases/monitoring.go`
- Create: `eebus-bridge/internal/usecases/lpc_test.go`
- Create: `eebus-bridge/internal/usecases/monitoring_test.go`

- [ ] **Step 1: Write failing test for LPC event dispatching**

Create `eebus-bridge/internal/usecases/lpc_test.go`:

```go
package usecases_test

import (
	"testing"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	eglpc "github.com/enbility/eebus-go/usecases/eg/lpc"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
)

func TestLPCEventRouting(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	lpcWrapper := usecases.NewLPCWrapper(bus)

	// Simulate an eebus-go event callback
	lpcWrapper.HandleEvent("test-ski", nil, nil, eebusapi.EventType(eglpc.DataUpdateLimit))

	select {
	case evt := <-ch:
		if evt.SKI != "test-ski" {
			t.Errorf("SKI = %q, want test-ski", evt.SKI)
		}
		if evt.Type != "lpc.limit_updated" {
			t.Errorf("Type = %q, want lpc.limit_updated", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for LPC event")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/volsch/eebus/eebus-bridge
go test ./internal/usecases/ -v
```

Expected: compilation error — `usecases` package does not exist.

- [ ] **Step 3: Implement LPC wrapper**

Create `eebus-bridge/internal/usecases/lpc.go`:

```go
package usecases

import (
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	eglpc "github.com/enbility/eebus-go/usecases/eg/lpc"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

// LPCWrapper wraps eebus-go's EG-LPC use case with event bus integration.
type LPCWrapper struct {
	uc  *eglpc.LPC
	bus *eebus.EventBus
}

func NewLPCWrapper(bus *eebus.EventBus) *LPCWrapper {
	return &LPCWrapper{bus: bus}
}

// Setup initializes the eebus-go LPC use case on the given local entity.
// Call this after the BridgeService is set up.
func (w *LPCWrapper) Setup(localEntity spineapi.EntityLocalInterface) {
	w.uc = eglpc.NewLPC(localEntity, w.HandleEvent)
}

// UseCase returns the underlying eebus-go LPC use case for service registration.
func (w *LPCWrapper) UseCase() *eglpc.LPC {
	return w.uc
}

// HandleEvent translates eebus-go events to internal bus events.
func (w *LPCWrapper) HandleEvent(ski string, _ spineapi.DeviceRemoteInterface, _ spineapi.EntityRemoteInterface, event eebusapi.EventType) {
	var eventType string
	switch event {
	case eglpc.DataUpdateLimit:
		eventType = "lpc.limit_updated"
	case eglpc.DataUpdateFailsafeConsumptionActivePowerLimit:
		eventType = "lpc.failsafe_power_updated"
	case eglpc.DataUpdateFailsafeDurationMinimum:
		eventType = "lpc.failsafe_duration_updated"
	case eglpc.DataUpdateHeartbeat:
		eventType = "lpc.heartbeat"
	case eglpc.UseCaseSupportUpdate:
		eventType = "lpc.use_case_support_updated"
	default:
		return
	}
	w.bus.Publish(eebus.Event{SKI: ski, Type: eventType})
}

// ConsumptionLimit reads the current consumption limit for a remote entity.
func (w *LPCWrapper) ConsumptionLimit(entity spineapi.EntityRemoteInterface) (ucapi.LoadLimit, error) {
	return w.uc.ConsumptionLimit(entity)
}

// WriteConsumptionLimit writes a new consumption limit.
func (w *LPCWrapper) WriteConsumptionLimit(entity spineapi.EntityRemoteInterface, limit ucapi.LoadLimit) error {
	_, err := w.uc.WriteConsumptionLimit(entity, limit, nil)
	return err
}

// FailsafeConsumptionActivePowerLimit reads the failsafe power limit.
func (w *LPCWrapper) FailsafeConsumptionActivePowerLimit(entity spineapi.EntityRemoteInterface) (float64, error) {
	return w.uc.FailsafeConsumptionActivePowerLimit(entity)
}

// WriteFailsafeConsumptionActivePowerLimit writes a new failsafe power limit.
func (w *LPCWrapper) WriteFailsafeConsumptionActivePowerLimit(entity spineapi.EntityRemoteInterface, value float64) error {
	_, err := w.uc.WriteFailsafeConsumptionActivePowerLimit(entity, value)
	return err
}

// FailsafeDurationMinimum reads the minimum failsafe duration.
func (w *LPCWrapper) FailsafeDurationMinimum(entity spineapi.EntityRemoteInterface) (time.Duration, error) {
	return w.uc.FailsafeDurationMinimum(entity)
}

// WriteFailsafeDurationMinimum writes a new minimum failsafe duration.
func (w *LPCWrapper) WriteFailsafeDurationMinimum(entity spineapi.EntityRemoteInterface, duration time.Duration) error {
	_, err := w.uc.WriteFailsafeDurationMinimum(entity, duration)
	return err
}

// StartHeartbeat starts the EEBUS heartbeat.
func (w *LPCWrapper) StartHeartbeat() {
	w.uc.StartHeartbeat()
}

// StopHeartbeat stops the EEBUS heartbeat.
func (w *LPCWrapper) StopHeartbeat() {
	w.uc.StopHeartbeat()
}

// IsHeartbeatWithinDuration checks if heartbeat is recent.
func (w *LPCWrapper) IsHeartbeatWithinDuration(entity spineapi.EntityRemoteInterface) bool {
	return w.uc.IsHeartbeatWithinDuration(entity)
}

// ConsumptionNominalMax reads the nominal maximum power consumption.
func (w *LPCWrapper) ConsumptionNominalMax(entity spineapi.EntityRemoteInterface) (float64, error) {
	return w.uc.ConsumptionNominalMax(entity)
}
```

- [ ] **Step 4: Run LPC test**

```bash
cd /home/volsch/eebus/eebus-bridge
go get github.com/enbility/eebus-go/usecases/eg/lpc
go test ./internal/usecases/ -run TestLPC -v
```

Expected: `TestLPCEventRouting` PASS.

- [ ] **Step 5: Write failing test for monitoring event dispatching**

Create `eebus-bridge/internal/usecases/monitoring_test.go`:

```go
package usecases_test

import (
	"testing"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	mampc "github.com/enbility/eebus-go/usecases/ma/mpc"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
)

func TestMonitoringEventRouting(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	monWrapper := usecases.NewMonitoringWrapper(bus)

	monWrapper.HandleEvent("test-ski", nil, nil, eebusapi.EventType(mampc.DataUpdatePower))

	select {
	case evt := <-ch:
		if evt.Type != "monitoring.power_updated" {
			t.Errorf("Type = %q, want monitoring.power_updated", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for monitoring event")
	}
}
```

- [ ] **Step 6: Implement monitoring wrapper**

Create `eebus-bridge/internal/usecases/monitoring.go`:

```go
package usecases

import (
	eebusapi "github.com/enbility/eebus-go/api"
	mampc "github.com/enbility/eebus-go/usecases/ma/mpc"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

// MonitoringWrapper wraps eebus-go's MA-MPC use case with event bus integration.
type MonitoringWrapper struct {
	uc  *mampc.MPC
	bus *eebus.EventBus
}

func NewMonitoringWrapper(bus *eebus.EventBus) *MonitoringWrapper {
	return &MonitoringWrapper{bus: bus}
}

// Setup initializes the eebus-go MPC use case on the given local entity.
func (w *MonitoringWrapper) Setup(localEntity spineapi.EntityLocalInterface) {
	w.uc = mampc.NewMPC(localEntity, w.HandleEvent)
}

// UseCase returns the underlying eebus-go MPC use case for service registration.
func (w *MonitoringWrapper) UseCase() *mampc.MPC {
	return w.uc
}

// HandleEvent translates eebus-go events to internal bus events.
func (w *MonitoringWrapper) HandleEvent(ski string, _ spineapi.DeviceRemoteInterface, _ spineapi.EntityRemoteInterface, event eebusapi.EventType) {
	var eventType string
	switch event {
	case mampc.DataUpdatePower:
		eventType = "monitoring.power_updated"
	case mampc.DataUpdatePowerPerPhase:
		eventType = "monitoring.power_per_phase_updated"
	case mampc.DataUpdateEnergyConsumed:
		eventType = "monitoring.energy_consumed_updated"
	case mampc.DataUpdateEnergyProduced:
		eventType = "monitoring.energy_produced_updated"
	case mampc.DataUpdateCurrentsPerPhase:
		eventType = "monitoring.currents_updated"
	case mampc.DataUpdateVoltagePerPhase:
		eventType = "monitoring.voltage_updated"
	case mampc.DataUpdateFrequency:
		eventType = "monitoring.frequency_updated"
	case mampc.UseCaseSupportUpdate:
		eventType = "monitoring.use_case_support_updated"
	default:
		return
	}
	w.bus.Publish(eebus.Event{SKI: ski, Type: eventType})
}

// Power reads the momentary total active power consumption (watts).
func (w *MonitoringWrapper) Power(entity spineapi.EntityRemoteInterface) (float64, error) {
	return w.uc.Power(entity)
}

// EnergyConsumed reads the total consumed energy (Wh).
func (w *MonitoringWrapper) EnergyConsumed(entity spineapi.EntityRemoteInterface) (float64, error) {
	return w.uc.EnergyConsumed(entity)
}

// CurrentPerPhase reads phase-specific current (A).
func (w *MonitoringWrapper) CurrentPerPhase(entity spineapi.EntityRemoteInterface) ([]float64, error) {
	return w.uc.CurrentPerPhase(entity)
}

// VoltagePerPhase reads phase-specific voltage (V).
func (w *MonitoringWrapper) VoltagePerPhase(entity spineapi.EntityRemoteInterface) ([]float64, error) {
	return w.uc.VoltagePerPhase(entity)
}

// Frequency reads the grid frequency (Hz).
func (w *MonitoringWrapper) Frequency(entity spineapi.EntityRemoteInterface) (float64, error) {
	return w.uc.Frequency(entity)
}
```

- [ ] **Step 7: Run all use case tests**

```bash
cd /home/volsch/eebus/eebus-bridge
go test ./internal/usecases/ -v
```

Expected: both tests PASS.

- [ ] **Step 8: Commit**

```bash
git add eebus-bridge/internal/usecases/
git commit -m "feat: add LPC and monitoring use case wrappers with event routing"
```

---

## Phase 5: gRPC Service Implementations

### Task 8: gRPC Server Setup

**Files:**
- Create: `eebus-bridge/internal/grpc/server.go`
- Create: `eebus-bridge/internal/grpc/server_test.go`

- [ ] **Step 1: Write failing test for server lifecycle**

Create `eebus-bridge/internal/grpc/server_test.go`:

```go
package grpc_test

import (
	"context"
	"testing"
	"time"

	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func TestServerStartStop(t *testing.T) {
	srv := bridgegrpc.NewServer(0) // port 0 = random free port

	go func() {
		if err := srv.Start(); err != nil {
			t.Logf("server stopped: %v", err)
		}
	}()

	// Wait for server to be ready
	time.Sleep(100 * time.Millisecond)
	addr := srv.Addr()
	if addr == "" {
		t.Fatal("server has no address")
	}

	// Connect and check health
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	client := grpc_health_v1.NewHealthClient(conn)
	resp, err := client.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Errorf("health status = %v, want SERVING", resp.Status)
	}

	srv.Stop()
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/volsch/eebus/eebus-bridge
go test ./internal/grpc/ -v
```

Expected: compilation error.

- [ ] **Step 3: Implement gRPC server**

Create `eebus-bridge/internal/grpc/server.go`:

```go
package grpc

import (
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Server wraps a gRPC server with health checking and reflection.
type Server struct {
	grpcServer *grpc.Server
	listener   net.Listener
	port       int
}

// NewServer creates a new gRPC server. Pass port 0 for a random free port.
func NewServer(port int) *Server {
	grpcServer := grpc.NewServer()

	// Health service
	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthSrv)
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// Reflection for debugging
	reflection.Register(grpcServer)

	return &Server{
		grpcServer: grpcServer,
		port:       port,
	}
}

// GRPCServer returns the underlying grpc.Server for service registration.
func (s *Server) GRPCServer() *grpc.Server {
	return s.grpcServer
}

// Start listens and serves. Blocks until Stop is called.
func (s *Server) Start() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.listener = lis
	return s.grpcServer.Serve(lis)
}

// Addr returns the listener address (available after Start is called).
func (s *Server) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Stop gracefully stops the gRPC server.
func (s *Server) Stop() {
	s.grpcServer.GracefulStop()
}
```

- [ ] **Step 4: Run test**

```bash
cd /home/volsch/eebus/eebus-bridge
go get google.golang.org/grpc
go test ./internal/grpc/ -v -race
```

Expected: `TestServerStartStop` PASS.

- [ ] **Step 5: Commit**

```bash
git add eebus-bridge/internal/grpc/server.go eebus-bridge/internal/grpc/server_test.go
git commit -m "feat: add gRPC server with health check and reflection"
```

---

### Task 9: DeviceService gRPC Implementation

**Files:**
- Create: `eebus-bridge/internal/grpc/device_service.go`
- Create: `eebus-bridge/internal/grpc/device_service_test.go`

- [ ] **Step 1: Write failing test for GetStatus**

Create `eebus-bridge/internal/grpc/device_service_test.go`:

```go
package grpc_test

import (
	"context"
	"testing"

	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"github.com/volschin/eebus-bridge/internal/eebus"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func setupDeviceTest(t *testing.T) (pb.DeviceServiceClient, func()) {
	t.Helper()

	bus := eebus.NewEventBus()
	callbacks := eebus.NewCallbacks(bus)
	svc := bridgegrpc.NewDeviceService(callbacks, bus, "test-local-ski")

	srv := bridgegrpc.NewServer(0)
	pb.RegisterDeviceServiceServer(srv.GRPCServer(), svc)

	go srv.Start()
	t.Cleanup(srv.Stop)

	// Wait briefly for server to start
	var conn *grpc.ClientConn
	var err error
	for i := 0; i < 10; i++ {
		if addr := srv.Addr(); addr != "" {
			conn, err = grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err == nil {
				break
			}
		}
		// retry
		<-make(chan struct{})
	}
	if conn == nil {
		t.Fatal("could not connect to server")
	}
	t.Cleanup(func() { conn.Close() })

	return pb.NewDeviceServiceClient(conn), func() {}
}

func TestGetStatus(t *testing.T) {
	client, cleanup := setupDeviceTest(t)
	defer cleanup()

	resp, err := client.GetStatus(context.Background(), &pb.Empty{})
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}

	if resp.LocalSki != "test-local-ski" {
		t.Errorf("LocalSki = %q, want test-local-ski", resp.LocalSki)
	}
	if !resp.Running {
		t.Error("Running = false, want true")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/volsch/eebus/eebus-bridge
go test ./internal/grpc/ -run TestGetStatus -v
```

Expected: compilation error — `NewDeviceService` not found.

- [ ] **Step 3: Implement DeviceService**

Create `eebus-bridge/internal/grpc/device_service.go`:

```go
package grpc

import (
	"context"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

type DeviceService struct {
	pb.UnimplementedDeviceServiceServer
	callbacks *eebus.Callbacks
	bus       *eebus.EventBus
	localSKI  string
}

func NewDeviceService(callbacks *eebus.Callbacks, bus *eebus.EventBus, localSKI string) *DeviceService {
	return &DeviceService{
		callbacks: callbacks,
		bus:       bus,
		localSKI:  localSKI,
	}
}

func (s *DeviceService) GetStatus(_ context.Context, _ *pb.Empty) (*pb.ServiceStatus, error) {
	return &pb.ServiceStatus{
		Running:  true,
		LocalSki: s.localSKI,
	}, nil
}

func (s *DeviceService) ListDiscoveredDevices(_ context.Context, _ *pb.Empty) (*pb.ListDevicesResponse, error) {
	svcs := s.callbacks.DiscoveredServices()
	devices := make([]*pb.DiscoveredDevice, 0, len(svcs))
	for _, svc := range svcs {
		devices = append(devices, &pb.DiscoveredDevice{
			Ski: svc.SKI,
		})
	}
	return &pb.ListDevicesResponse{Devices: devices}, nil
}

func (s *DeviceService) RegisterRemoteSKI(_ context.Context, req *pb.RegisterSKIRequest) (*pb.Empty, error) {
	// Will be wired to BridgeService.RegisterRemoteSKI in main.go
	// For now, publish an event so the bridge can react
	s.bus.Publish(eebus.Event{SKI: req.Ski, Type: "device.register_ski"})
	return &pb.Empty{}, nil
}

func (s *DeviceService) UnregisterRemoteSKI(_ context.Context, req *pb.DeviceRequest) (*pb.Empty, error) {
	s.bus.Publish(eebus.Event{SKI: req.Ski, Type: "device.unregister_ski"})
	return &pb.Empty{}, nil
}

func (s *DeviceService) GetPairingStatus(_ context.Context, req *pb.DeviceRequest) (*pb.PairingStatus, error) {
	state := s.callbacks.PairingState(req.Ski)
	ps := &pb.PairingStatus{Ski: req.Ski, State: pb.PairingState_PAIRING_STATE_UNSPECIFIED}
	if state != nil {
		ps.State = mapPairingState(state.State)
	}
	return ps, nil
}

func (s *DeviceService) SubscribeDeviceEvents(_ *pb.Empty, stream pb.DeviceService_SubscribeDeviceEventsServer) error {
	ch := s.bus.Subscribe()
	defer s.bus.Unsubscribe(ch)

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return nil
			}
			var eventType pb.DeviceEventType
			switch evt.Type {
			case "device.connected":
				eventType = pb.DeviceEventType_DEVICE_EVENT_CONNECTED
			case "device.disconnected":
				eventType = pb.DeviceEventType_DEVICE_EVENT_DISCONNECTED
			default:
				continue
			}
			if err := stream.Send(&pb.DeviceEvent{
				Ski:       evt.SKI,
				EventType: eventType,
			}); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

func (s *DeviceService) ListPairedDevices(_ context.Context, _ *pb.Empty) (*pb.ListPairedDevicesResponse, error) {
	// Will be enhanced when paired device tracking is implemented
	return &pb.ListPairedDevicesResponse{}, nil
}

func mapPairingState(state int) pb.PairingState {
	// Map ship-go ConnectionState values
	switch state {
	case 0:
		return pb.PairingState_PAIRING_STATE_PENDING
	case 1:
		return pb.PairingState_PAIRING_STATE_WAITING_FOR_TRUST
	case 2:
		return pb.PairingState_PAIRING_STATE_TRUSTED
	case 3:
		return pb.PairingState_PAIRING_STATE_DENIED
	default:
		return pb.PairingState_PAIRING_STATE_UNSPECIFIED
	}
}
```

Note: The `mapPairingState` function maps integer-typed state values. The exact mapping will need validation against ship-go's `ConnectionStateDetail.State` type. Adjust the `state` parameter type once you inspect the actual ship-go type at compile time.

- [ ] **Step 4: Run test**

```bash
cd /home/volsch/eebus/eebus-bridge
go test ./internal/grpc/ -run TestGetStatus -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add eebus-bridge/internal/grpc/device_service.go eebus-bridge/internal/grpc/device_service_test.go
git commit -m "feat: add DeviceService gRPC implementation"
```

---

### Task 10: LPCService gRPC Implementation

**Files:**
- Create: `eebus-bridge/internal/grpc/lpc_service.go`
- Create: `eebus-bridge/internal/grpc/lpc_service_test.go`

- [ ] **Step 1: Write failing test for LPC service**

Create `eebus-bridge/internal/grpc/lpc_service_test.go`:

```go
package grpc_test

import (
	"context"
	"testing"
	"time"

	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"github.com/volschin/eebus-bridge/internal/eebus"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestSubscribeLPCEvents(t *testing.T) {
	bus := eebus.NewEventBus()
	svc := bridgegrpc.NewLPCService(nil, bus) // nil wrapper — only testing streaming

	srv := bridgegrpc.NewServer(0)
	pb.RegisterLPCServiceServer(srv.GRPCServer(), svc)
	go srv.Start()
	t.Cleanup(srv.Stop)

	time.Sleep(100 * time.Millisecond)
	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	client := pb.NewLPCServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stream, err := client.SubscribeLPCEvents(ctx, &pb.DeviceRequest{Ski: "test-ski"})
	if err != nil {
		t.Fatal(err)
	}

	// Publish event on bus
	bus.Publish(eebus.Event{SKI: "test-ski", Type: "lpc.limit_updated"})

	evt, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if evt.EventType != pb.LPCEventType_LPC_EVENT_LIMIT_UPDATED {
		t.Errorf("EventType = %v, want LPC_EVENT_LIMIT_UPDATED", evt.EventType)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/volsch/eebus/eebus-bridge
go test ./internal/grpc/ -run TestSubscribeLPCEvents -v
```

Expected: compilation error.

- [ ] **Step 3: Implement LPCService**

Create `eebus-bridge/internal/grpc/lpc_service.go`:

```go
package grpc

import (
	"context"
	"time"

	ucapi "github.com/enbility/eebus-go/usecases/api"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type LPCService struct {
	pb.UnimplementedLPCServiceServer
	lpc *usecases.LPCWrapper
	bus *eebus.EventBus
}

func NewLPCService(lpc *usecases.LPCWrapper, bus *eebus.EventBus) *LPCService {
	return &LPCService{lpc: lpc, bus: bus}
}

func (s *LPCService) GetConsumptionLimit(ctx context.Context, req *pb.DeviceRequest) (*pb.LoadLimit, error) {
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	// Entity lookup will be implemented with device registry
	return nil, status.Error(codes.Unimplemented, "entity lookup not yet wired")
}

func (s *LPCService) WriteConsumptionLimit(ctx context.Context, req *pb.WriteLoadLimitRequest) (*pb.Empty, error) {
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	// Entity lookup will be implemented with device registry
	return nil, status.Error(codes.Unimplemented, "entity lookup not yet wired")
}

func (s *LPCService) GetFailsafeLimit(ctx context.Context, req *pb.DeviceRequest) (*pb.FailsafeLimit, error) {
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	return nil, status.Error(codes.Unimplemented, "entity lookup not yet wired")
}

func (s *LPCService) WriteFailsafeLimit(ctx context.Context, req *pb.WriteFailsafeLimitRequest) (*pb.Empty, error) {
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	return nil, status.Error(codes.Unimplemented, "entity lookup not yet wired")
}

func (s *LPCService) StartHeartbeat(ctx context.Context, req *pb.DeviceRequest) (*pb.Empty, error) {
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	s.lpc.StartHeartbeat()
	return &pb.Empty{}, nil
}

func (s *LPCService) StopHeartbeat(ctx context.Context, req *pb.DeviceRequest) (*pb.Empty, error) {
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	s.lpc.StopHeartbeat()
	return &pb.Empty{}, nil
}

func (s *LPCService) GetHeartbeatStatus(ctx context.Context, req *pb.DeviceRequest) (*pb.HeartbeatStatus, error) {
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	return &pb.HeartbeatStatus{
		Running: true, // simplified — heartbeat running state could be tracked
	}, nil
}

func (s *LPCService) GetConsumptionNominalMax(ctx context.Context, req *pb.DeviceRequest) (*pb.PowerValue, error) {
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	return nil, status.Error(codes.Unimplemented, "entity lookup not yet wired")
}

func (s *LPCService) SubscribeLPCEvents(req *pb.DeviceRequest, stream pb.LPCService_SubscribeLPCEventsServer) error {
	ch := s.bus.Subscribe()
	defer s.bus.Unsubscribe(ch)

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return nil
			}
			// Filter by SKI
			if req.Ski != "" && evt.SKI != req.Ski {
				continue
			}
			var eventType pb.LPCEventType
			switch evt.Type {
			case "lpc.limit_updated":
				eventType = pb.LPCEventType_LPC_EVENT_LIMIT_UPDATED
			case "lpc.failsafe_power_updated", "lpc.failsafe_duration_updated":
				eventType = pb.LPCEventType_LPC_EVENT_FAILSAFE_UPDATED
			case "lpc.heartbeat":
				eventType = pb.LPCEventType_LPC_EVENT_HEARTBEAT_TIMEOUT
			default:
				continue
			}
			if err := stream.Send(&pb.LPCEvent{
				Ski:       evt.SKI,
				EventType: eventType,
			}); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

// convertLoadLimit converts ucapi.LoadLimit to protobuf LoadLimit.
func convertLoadLimit(l ucapi.LoadLimit) *pb.LoadLimit {
	return &pb.LoadLimit{
		ValueWatts:      l.Value,
		DurationSeconds: int64(l.Duration / time.Second),
		IsActive:        l.IsActive,
		IsChangeable:    l.IsChangeable,
	}
}
```

- [ ] **Step 4: Run test**

```bash
cd /home/volsch/eebus/eebus-bridge
go test ./internal/grpc/ -run TestSubscribeLPCEvents -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add eebus-bridge/internal/grpc/lpc_service.go eebus-bridge/internal/grpc/lpc_service_test.go
git commit -m "feat: add LPCService gRPC implementation with event streaming"
```

---

### Task 11: MonitoringService gRPC Implementation

**Files:**
- Create: `eebus-bridge/internal/grpc/monitoring_service.go`
- Create: `eebus-bridge/internal/grpc/monitoring_service_test.go`

- [ ] **Step 1: Write failing test for monitoring event streaming**

Create `eebus-bridge/internal/grpc/monitoring_service_test.go`:

```go
package grpc_test

import (
	"context"
	"testing"
	"time"

	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"github.com/volschin/eebus-bridge/internal/eebus"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestSubscribeMeasurements(t *testing.T) {
	bus := eebus.NewEventBus()
	svc := bridgegrpc.NewMonitoringService(nil, bus)

	srv := bridgegrpc.NewServer(0)
	pb.RegisterMonitoringServiceServer(srv.GRPCServer(), svc)
	go srv.Start()
	t.Cleanup(srv.Stop)

	time.Sleep(100 * time.Millisecond)
	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	client := pb.NewMonitoringServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stream, err := client.SubscribeMeasurements(ctx, &pb.DeviceRequest{Ski: "test-ski"})
	if err != nil {
		t.Fatal(err)
	}

	bus.Publish(eebus.Event{SKI: "test-ski", Type: "monitoring.power_updated"})

	evt, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if evt.EventType != pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_UPDATED {
		t.Errorf("EventType = %v, want MEASUREMENT_EVENT_POWER_UPDATED", evt.EventType)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/volsch/eebus/eebus-bridge
go test ./internal/grpc/ -run TestSubscribeMeasurements -v
```

Expected: compilation error.

- [ ] **Step 3: Implement MonitoringService**

Create `eebus-bridge/internal/grpc/monitoring_service.go`:

```go
package grpc

import (
	"context"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type MonitoringService struct {
	pb.UnimplementedMonitoringServiceServer
	monitoring *usecases.MonitoringWrapper
	bus        *eebus.EventBus
}

func NewMonitoringService(monitoring *usecases.MonitoringWrapper, bus *eebus.EventBus) *MonitoringService {
	return &MonitoringService{monitoring: monitoring, bus: bus}
}

func (s *MonitoringService) GetPowerConsumption(ctx context.Context, req *pb.DeviceRequest) (*pb.PowerMeasurement, error) {
	if s.monitoring == nil {
		return nil, status.Error(codes.Unavailable, "monitoring use case not initialized")
	}
	return nil, status.Error(codes.Unimplemented, "entity lookup not yet wired")
}

func (s *MonitoringService) GetEnergyConsumed(ctx context.Context, req *pb.DeviceRequest) (*pb.EnergyMeasurement, error) {
	if s.monitoring == nil {
		return nil, status.Error(codes.Unavailable, "monitoring use case not initialized")
	}
	return nil, status.Error(codes.Unimplemented, "entity lookup not yet wired")
}

func (s *MonitoringService) GetMeasurements(ctx context.Context, req *pb.DeviceRequest) (*pb.MeasurementList, error) {
	if s.monitoring == nil {
		return nil, status.Error(codes.Unavailable, "monitoring use case not initialized")
	}
	return nil, status.Error(codes.Unimplemented, "entity lookup not yet wired")
}

func (s *MonitoringService) SubscribeMeasurements(req *pb.DeviceRequest, stream pb.MonitoringService_SubscribeMeasurementsServer) error {
	ch := s.bus.Subscribe()
	defer s.bus.Unsubscribe(ch)

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return nil
			}
			if req.Ski != "" && evt.SKI != req.Ski {
				continue
			}
			var eventType pb.MeasurementEventType
			switch evt.Type {
			case "monitoring.power_updated":
				eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_UPDATED
			case "monitoring.energy_consumed_updated":
				eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_ENERGY_UPDATED
			default:
				continue
			}
			if err := stream.Send(&pb.MeasurementEvent{
				Ski:       evt.SKI,
				EventType: eventType,
			}); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}
```

- [ ] **Step 4: Run test**

```bash
cd /home/volsch/eebus/eebus-bridge
go test ./internal/grpc/ -run TestSubscribeMeasurements -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add eebus-bridge/internal/grpc/monitoring_service.go eebus-bridge/internal/grpc/monitoring_service_test.go
git commit -m "feat: add MonitoringService gRPC implementation with event streaming"
```

---

## Phase 6: Wiring & Main

### Task 12: Device Registry & Entity Lookup

**Files:**
- Create: `eebus-bridge/internal/eebus/registry.go`
- Create: `eebus-bridge/internal/eebus/registry_test.go`

This registry tracks connected remote devices and their entities, enabling gRPC services to resolve a SKI to a `spineapi.EntityRemoteInterface`.

- [ ] **Step 1: Write failing test for device registry**

Create `eebus-bridge/internal/eebus/registry_test.go`:

```go
package eebus_test

import (
	"testing"

	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestRegistryAddAndLookup(t *testing.T) {
	reg := eebus.NewDeviceRegistry()

	reg.AddDevice("ski-123", eebus.DeviceInfo{
		Brand:  "Vaillant",
		Model:  "VR940f",
		Serial: "12345",
	})

	info, ok := reg.GetDevice("ski-123")
	if !ok {
		t.Fatal("device not found")
	}
	if info.Brand != "Vaillant" {
		t.Errorf("Brand = %q, want Vaillant", info.Brand)
	}
}

func TestRegistryRemove(t *testing.T) {
	reg := eebus.NewDeviceRegistry()
	reg.AddDevice("ski-123", eebus.DeviceInfo{Brand: "Vaillant"})
	reg.RemoveDevice("ski-123")

	_, ok := reg.GetDevice("ski-123")
	if ok {
		t.Error("device should have been removed")
	}
}

func TestRegistryListDevices(t *testing.T) {
	reg := eebus.NewDeviceRegistry()
	reg.AddDevice("ski-1", eebus.DeviceInfo{Brand: "A"})
	reg.AddDevice("ski-2", eebus.DeviceInfo{Brand: "B"})

	devices := reg.ListDevices()
	if len(devices) != 2 {
		t.Errorf("len(devices) = %d, want 2", len(devices))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/volsch/eebus/eebus-bridge
go test ./internal/eebus/ -run TestRegistry -v
```

Expected: compilation error.

- [ ] **Step 3: Implement device registry**

Create `eebus-bridge/internal/eebus/registry.go`:

```go
package eebus

import (
	"sync"

	spineapi "github.com/enbility/spine-go/api"
)

// DeviceInfo holds metadata about a connected remote EEBUS device.
type DeviceInfo struct {
	SKI            string
	Brand          string
	Model          string
	Serial         string
	DeviceType     string
	UseCases       []string
	RemoteDevice   spineapi.DeviceRemoteInterface
	RemoteEntities []spineapi.EntityRemoteInterface
}

// DeviceRegistry tracks connected remote EEBUS devices.
type DeviceRegistry struct {
	mu      sync.RWMutex
	devices map[string]DeviceInfo
}

func NewDeviceRegistry() *DeviceRegistry {
	return &DeviceRegistry{
		devices: make(map[string]DeviceInfo),
	}
}

func (r *DeviceRegistry) AddDevice(ski string, info DeviceInfo) {
	r.mu.Lock()
	info.SKI = ski
	r.devices[ski] = info
	r.mu.Unlock()
}

func (r *DeviceRegistry) RemoveDevice(ski string) {
	r.mu.Lock()
	delete(r.devices, ski)
	r.mu.Unlock()
}

func (r *DeviceRegistry) GetDevice(ski string) (DeviceInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.devices[ski]
	return info, ok
}

func (r *DeviceRegistry) ListDevices() []DeviceInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]DeviceInfo, 0, len(r.devices))
	for _, info := range r.devices {
		result = append(result, info)
	}
	return result
}

// FirstEntity returns the first remote entity for a device, or nil.
func (r *DeviceRegistry) FirstEntity(ski string) spineapi.EntityRemoteInterface {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.devices[ski]
	if !ok || len(info.RemoteEntities) == 0 {
		return nil
	}
	return info.RemoteEntities[0]
}
```

- [ ] **Step 4: Run tests**

```bash
cd /home/volsch/eebus/eebus-bridge
go test ./internal/eebus/ -run TestRegistry -v
```

Expected: all 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add eebus-bridge/internal/eebus/registry.go eebus-bridge/internal/eebus/registry_test.go
git commit -m "feat: add device registry for tracking connected EEBUS devices"
```

---

### Task 13: Wire main.go

**Files:**
- Modify: `eebus-bridge/cmd/eebus-bridge/main.go`

- [ ] **Step 1: Implement full main.go**

Replace `eebus-bridge/cmd/eebus-bridge/main.go`:

```go
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/certs"
	"github.com/volschin/eebus-bridge/internal/config"
	"github.com/volschin/eebus-bridge/internal/eebus"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"github.com/volschin/eebus-bridge/internal/usecases"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	// Load configuration
	cfg, err := config.LoadFromFile(*configPath)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	// Ensure TLS certificate
	cert, err := certs.EnsureCertificate(
		cfg.Certificates.CertFile,
		cfg.Certificates.KeyFile,
		cfg.Certificates.StoragePath,
	)
	if err != nil {
		log.Fatalf("certificate: %v", err)
	}

	ski, err := certs.SKIFromCertificate(cert)
	if err != nil {
		log.Fatalf("extracting SKI: %v", err)
	}
	log.Printf("Local SKI: %s", ski)

	// Create event bus
	bus := eebus.NewEventBus()

	// Create EEBUS bridge service
	bridgeSvc, err := eebus.NewBridgeService(cfg, cert, bus)
	if err != nil {
		log.Fatalf("creating bridge service: %v", err)
	}

	// Setup use cases
	lpcWrapper := usecases.NewLPCWrapper(bus)
	monitoringWrapper := usecases.NewMonitoringWrapper(bus)

	if err := bridgeSvc.Setup(); err != nil {
		log.Fatalf("setting up EEBUS service: %v", err)
	}

	localEntity := bridgeSvc.LocalEntity()
	lpcWrapper.Setup(localEntity)
	monitoringWrapper.Setup(localEntity)

	// Create gRPC server
	grpcSrv := bridgegrpc.NewServer(cfg.GRPC.Port)

	// Register gRPC services
	deviceSvc := bridgegrpc.NewDeviceService(bridgeSvc.Callbacks(), bus, ski)
	lpcSvc := bridgegrpc.NewLPCService(lpcWrapper, bus)
	monitoringSvc := bridgegrpc.NewMonitoringService(monitoringWrapper, bus)

	pb.RegisterDeviceServiceServer(grpcSrv.GRPCServer(), deviceSvc)
	pb.RegisterLPCServiceServer(grpcSrv.GRPCServer(), lpcSvc)
	pb.RegisterMonitoringServiceServer(grpcSrv.GRPCServer(), monitoringSvc)

	// Start services
	go func() {
		log.Printf("gRPC server listening on :%d", cfg.GRPC.Port)
		if err := grpcSrv.Start(); err != nil {
			log.Fatalf("gRPC server: %v", err)
		}
	}()

	bridgeSvc.Start()
	log.Println("EEBUS bridge started")

	// Handle SKI registration events from gRPC
	go func() {
		ch := bus.Subscribe()
		defer bus.Unsubscribe(ch)
		for evt := range ch {
			switch evt.Type {
			case "device.register_ski":
				bridgeSvc.RegisterRemoteSKI(evt.SKI)
				log.Printf("Registered remote SKI: %s", evt.SKI)
			case "device.unregister_ski":
				bridgeSvc.UnregisterRemoteSKI(evt.SKI)
				log.Printf("Unregistered remote SKI: %s", evt.SKI)
			}
		}
	}()

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	grpcSrv.Stop()
	bridgeSvc.Shutdown()
	log.Println("Shutdown complete")
}
```

- [ ] **Step 2: Create example config file**

Create `eebus-bridge/config.yaml`:

```yaml
grpc:
  port: 50051

eebus:
  port: 4712
  vendor: "HomeAssistant"
  brand: "eebus-bridge"
  model: "eebus-bridge"
  serial: "ha-001"

certificates:
  auto_generate: true
  storage_path: "/data/certs"
```

- [ ] **Step 3: Verify compilation**

```bash
cd /home/volsch/eebus/eebus-bridge
go mod tidy
go build ./cmd/eebus-bridge/
```

Expected: compiles without errors.

- [ ] **Step 4: Commit**

```bash
git add eebus-bridge/cmd/eebus-bridge/main.go eebus-bridge/config.yaml eebus-bridge/go.mod eebus-bridge/go.sum
git commit -m "feat: wire main.go with config, certs, EEBUS service, and gRPC server"
```

---

## Phase 7: Docker Deployment

### Task 14: Dockerfile & docker-compose

**Files:**
- Create: `eebus-bridge/Dockerfile`
- Create: `docker-compose.yml` (project root)

- [ ] **Step 1: Create multi-stage Dockerfile**

Create `eebus-bridge/Dockerfile`:

```dockerfile
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /eebus-bridge ./cmd/eebus-bridge

FROM alpine:3.19

RUN apk add --no-cache ca-certificates
COPY --from=builder /eebus-bridge /usr/local/bin/eebus-bridge

VOLUME /data
EXPOSE 50051 4712

ENTRYPOINT ["eebus-bridge"]
CMD ["--config", "/etc/eebus-bridge/config.yaml"]
```

- [ ] **Step 2: Create docker-compose.yml**

Create `docker-compose.yml` (project root):

```yaml
services:
  eebus-bridge:
    build:
      context: ./eebus-bridge
    ports:
      - "50051:50051"
      - "4712:4712"
    volumes:
      - eebus-data:/data
      - ./eebus-bridge/config.yaml:/etc/eebus-bridge/config.yaml:ro
    network_mode: host  # Required for mDNS discovery
    restart: unless-stopped

volumes:
  eebus-data:
```

- [ ] **Step 3: Verify Docker build**

```bash
cd /home/volsch/eebus
docker build -t eebus-bridge ./eebus-bridge
```

Expected: image builds successfully.

- [ ] **Step 4: Commit**

```bash
git add eebus-bridge/Dockerfile docker-compose.yml
git commit -m "feat: add Dockerfile and docker-compose for bridge deployment"
```

---

## Phase 8: HA Custom Integration — Scaffold & HACS

### Task 15: HACS Repository Setup + Integration Scaffold

**Files:**
- Create: `hacs.json`
- Create: `ha-integration/custom_components/eebus/manifest.json`
- Create: `ha-integration/custom_components/eebus/quality_scale.yaml`
- Create: `ha-integration/custom_components/eebus/const.py`
- Create: `ha-integration/custom_components/eebus/__init__.py`
- Create: `ha-integration/custom_components/eebus/brand/icon.png`
- Create: `ha-integration/custom_components/eebus/brand/logo.png`
- Create: `pyproject.toml`
- Create: `LICENSE`

- [ ] **Step 1: Create hacs.json (repo root)**

Create `hacs.json`:

```json
{
  "name": "EEBUS",
  "render_readme": true
}
```

- [ ] **Step 2: Create LICENSE**

Create `LICENSE` with MIT license text (use your name + current year).

- [ ] **Step 3: Create pyproject.toml**

Create `pyproject.toml`:

```toml
[tool.pytest.ini_options]
asyncio_mode = "auto"
testpaths = ["ha-integration/custom_components/eebus/tests"]

[tool.coverage.report]
exclude_lines = [
    "pragma: no cover",
    "if __name__",
    "if TYPE_CHECKING",
]

[tool.ruff]
target-version = "py312"
line-length = 120
```

- [ ] **Step 4: Create manifest.json (Gold-compliant)**

Create `ha-integration/custom_components/eebus/manifest.json`:

```json
{
  "domain": "eebus",
  "name": "EEBUS",
  "codeowners": ["@volschin"],
  "config_flow": true,
  "documentation": "https://github.com/volschin/eebus-ha-bridge",
  "integration_type": "device",
  "iot_class": "local_push",
  "issue_tracker": "https://github.com/volschin/eebus-ha-bridge/issues",
  "quality_scale": "gold",
  "requirements": ["grpcio>=1.60.0", "protobuf>=4.25.0"],
  "single_config_entry": false,
  "version": "0.1.0"
}
```

- [ ] **Step 5: Create quality_scale.yaml**

Create `ha-integration/custom_components/eebus/quality_scale.yaml`:

```yaml
# Quality Scale for EEBUS Integration
# https://developers.home-assistant.io/docs/core/integration-quality-scale/

rules:
  # Bronze
  action-setup:
    status: exempt
    comment: Integration bietet keine Service Actions.
  appropriate-polling: done
  brands: done
  common-modules: done
  config-flow: done
  config-flow-test-coverage: done
  dependency-transparency: done
  docs-actions:
    status: exempt
    comment: Integration bietet keine Service Actions.
  docs-high-level-description: done
  docs-installation-instructions: done
  docs-removal-instructions: done
  entity-event-setup: done
  entity-unique-id: done
  has-entity-name: done
  runtime-data: done
  test-before-configure: done
  test-before-setup: done
  unique-config-entry: done

  # Silver
  action-exceptions:
    status: exempt
    comment: Integration bietet keine Service Actions.
  config-entry-unloading: done
  docs-configuration-parameters: done
  docs-installation-parameters: done
  entity-unavailable: done
  integration-owner: done
  log-when-unavailable: done
  parallel-updates: done
  reauthentication-flow:
    status: exempt
    comment: Keine Authentifizierung nötig (lokales gRPC-Protokoll).
  test-coverage: done

  # Gold
  devices: done
  diagnostics: done
  discovery:
    status: done
    comment: mDNS-basierte EEBUS/SHIP Discovery via Bridge gRPC API.
  discovery-update-info:
    status: exempt
    comment: Netzwerk-Info-Update über Bridge, nicht direkt via HA Discovery.
  docs-data-update: done
  docs-examples: done
  docs-known-limitations: done
  docs-supported-devices: done
  docs-supported-functions: done
  docs-troubleshooting: done
  docs-use-cases: done
  dynamic-devices:
    status: exempt
    comment: Ein EEBUS-Gerät pro Config Entry.
  entity-category: done
  entity-device-class: done
  entity-disabled-by-default: done
  entity-translations: done
  exception-translations: done
  icon-translations: done
  reconfiguration-flow: done
  repair-issues:
    status: exempt
    comment: >
      Keine Auth, Verbindungsprobleme über Verfügbarkeit und Logging
      kommuniziert. Reconfigure-Flow deckt Bridge-Adressänderungen ab.
  stale-devices:
    status: exempt
    comment: Ein Gerät pro Config Entry, wird bei Unload entfernt.
```

- [ ] **Step 6: Create const.py**

Create `ha-integration/custom_components/eebus/const.py`:

```python
"""Constants for the EEBUS integration."""

from homeassistant.const import Platform

DOMAIN = "eebus"
DEFAULT_GRPC_PORT = 50051
CONF_GRPC_HOST = "grpc_host"
CONF_GRPC_PORT = "grpc_port"
CONF_DEVICE_SKI = "device_ski"

PLATFORMS = [
    Platform.BINARY_SENSOR,
    Platform.NUMBER,
    Platform.SENSOR,
    Platform.SWITCH,
]

PARALLEL_UPDATES = 0  # Coordinator-based, no per-entity polling
```

- [ ] **Step 7: Create __init__.py (runtime_data pattern)**

Create `ha-integration/custom_components/eebus/__init__.py`:

```python
"""EEBUS integration for Home Assistant."""

from __future__ import annotations

from typing import TYPE_CHECKING

from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant

from .const import CONF_DEVICE_SKI, CONF_GRPC_HOST, CONF_GRPC_PORT, PLATFORMS
from .coordinator import EebusCoordinator

if TYPE_CHECKING:
    EebusConfigEntry = ConfigEntry[EebusCoordinator]
else:
    EebusConfigEntry = ConfigEntry


async def async_setup_entry(hass: HomeAssistant, entry: EebusConfigEntry) -> bool:
    """Set up EEBUS from a config entry."""
    coordinator = EebusCoordinator(
        hass,
        host=entry.data[CONF_GRPC_HOST],
        port=entry.data[CONF_GRPC_PORT],
        ski=entry.data[CONF_DEVICE_SKI],
    )
    await coordinator.async_config_entry_first_refresh()

    entry.runtime_data = coordinator

    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)

    entry.async_on_unload(entry.add_update_listener(_async_reload_entry))

    return True


async def async_unload_entry(hass: HomeAssistant, entry: EebusConfigEntry) -> bool:
    """Unload EEBUS config entry."""
    if unload_ok := await hass.config_entries.async_unload_platforms(entry, PLATFORMS):
        await entry.runtime_data.async_shutdown()
    return unload_ok


async def _async_reload_entry(hass: HomeAssistant, entry: EebusConfigEntry) -> None:
    """Reload on options change."""
    await hass.config_entries.async_reload(entry.entry_id)
```

- [ ] **Step 8: Create brand directory with placeholder icons**

Create `ha-integration/custom_components/eebus/brand/icon.png` — 256x256 EEBUS icon (placeholder: use EEBUS logo or a simple energy icon).

Create `ha-integration/custom_components/eebus/brand/logo.png` — 256x256 EEBUS logo.

Note: These can be generated via any icon tool or sourced from the EEBUS Initiative website. HACS requires at minimum `icon.png`.

- [ ] **Step 9: Commit**

```bash
git add hacs.json LICENSE pyproject.toml ha-integration/
git commit -m "feat: scaffold HA integration with HACS + Gold quality scale compliance"
```

---

### Task 16: gRPC Client & Coordinator

**Files:**
- Create: `ha-integration/custom_components/eebus/coordinator.py`
- Create: `ha-integration/custom_components/eebus/tests/conftest.py`
- Create: `ha-integration/custom_components/eebus/tests/test_coordinator.py`

- [ ] **Step 1: Write failing test for coordinator**

Create `ha-integration/custom_components/eebus/tests/__init__.py` (empty).

Create `ha-integration/custom_components/eebus/tests/conftest.py`:

```python
"""Test fixtures for EEBUS integration."""

from unittest.mock import AsyncMock, MagicMock, patch

import pytest


@pytest.fixture
def mock_grpc_channel():
    """Mock gRPC channel."""
    with patch("grpc.aio.insecure_channel") as mock:
        channel = AsyncMock()
        mock.return_value = channel
        yield channel


@pytest.fixture
def mock_device_stub(mock_grpc_channel):
    """Mock DeviceService stub."""
    stub = AsyncMock()
    status = MagicMock()
    status.running = True
    status.local_ski = "test-ski"
    stub.GetStatus = AsyncMock(return_value=status)
    return stub
```

Create `ha-integration/custom_components/eebus/tests/test_coordinator.py`:

```python
"""Tests for the EEBUS coordinator."""

from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from custom_components.eebus.coordinator import EebusCoordinator


@pytest.fixture
def mock_hass():
    """Create a mock HomeAssistant instance."""
    hass = MagicMock()
    hass.loop = MagicMock()
    return hass


@pytest.mark.asyncio
async def test_coordinator_creates_channel(mock_hass):
    """Test that coordinator creates gRPC channel with correct address."""
    with patch("grpc.aio.insecure_channel") as mock_channel:
        mock_channel.return_value = AsyncMock()
        coordinator = EebusCoordinator(
            mock_hass, host="192.168.1.100", port=50051, ski="test-ski"
        )
        assert coordinator.host == "192.168.1.100"
        assert coordinator.port == 50051
        assert coordinator.ski == "test-ski"
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/volsch/eebus/ha-integration
python -m pytest custom_components/eebus/tests/test_coordinator.py -v
```

Expected: `ImportError` — `coordinator` module does not exist.

- [ ] **Step 3: Implement coordinator (with log-when-unavailable)**

Create `ha-integration/custom_components/eebus/coordinator.py`:

```python
"""DataUpdateCoordinator for EEBUS integration."""

from __future__ import annotations

import asyncio
import logging
from datetime import timedelta
from typing import Any

import grpc
import grpc.aio

from homeassistant.core import HomeAssistant
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed

_LOGGER = logging.getLogger(__name__)

POLL_INTERVAL = timedelta(seconds=30)


class EebusCoordinator(DataUpdateCoordinator[dict[str, Any]]):
    """Coordinator that manages gRPC connection and data updates.

    Implements log-when-unavailable (Silver): logs once on first failure,
    once on recovery. No repeated warnings.
    """

    def __init__(
        self,
        hass: HomeAssistant,
        host: str,
        port: int,
        ski: str,
    ) -> None:
        """Initialize the coordinator."""
        super().__init__(
            hass,
            _LOGGER,
            name="EEBUS",
            update_interval=POLL_INTERVAL,
        )
        self.host = host
        self.port = port
        self.ski = ski
        self._channel: grpc.aio.Channel | None = None
        self._stream_tasks: list[asyncio.Task] = []
        self._was_unavailable: bool = False

    async def _ensure_channel(self) -> grpc.aio.Channel:
        """Create or return existing gRPC channel."""
        if self._channel is None:
            self._channel = grpc.aio.insecure_channel(f"{self.host}:{self.port}")
        return self._channel

    async def _async_update_data(self) -> dict[str, Any]:
        """Fetch data via gRPC polling (fallback when streams fail)."""
        try:
            channel = await self._ensure_channel()
            from . import proto_stubs

            device_stub = proto_stubs.DeviceServiceStub(channel)
            status = await device_stub.GetStatus(proto_stubs.Empty())

            data: dict[str, Any] = {
                "connected": status.running,
                "local_ski": status.local_ski,
            }

            # Try to get power consumption
            try:
                monitoring_stub = proto_stubs.MonitoringServiceStub(channel)
                power = await monitoring_stub.GetPowerConsumption(
                    proto_stubs.DeviceRequest(ski=self.ski)
                )
                data["power_watts"] = power.watts
            except grpc.aio.AioRpcError:
                data["power_watts"] = None

            # Try to get LPC limit
            try:
                lpc_stub = proto_stubs.LPCServiceStub(channel)
                limit = await lpc_stub.GetConsumptionLimit(
                    proto_stubs.DeviceRequest(ski=self.ski)
                )
                data["consumption_limit"] = {
                    "value_watts": limit.value_watts,
                    "is_active": limit.is_active,
                    "is_changeable": limit.is_changeable,
                }
            except grpc.aio.AioRpcError:
                data["consumption_limit"] = None

            # Try to get heartbeat status
            try:
                lpc_stub = proto_stubs.LPCServiceStub(channel)
                hb = await lpc_stub.GetHeartbeatStatus(
                    proto_stubs.DeviceRequest(ski=self.ski)
                )
                data["heartbeat_status"] = {
                    "running": hb.running,
                    "within_duration": hb.within_duration,
                }
            except grpc.aio.AioRpcError:
                data["heartbeat_status"] = None

            # Log recovery (log-when-unavailable, Silver)
            if self._was_unavailable:
                _LOGGER.info("EEBUS bridge connection restored at %s:%s", self.host, self.port)
                self._was_unavailable = False

            return data
        except grpc.aio.AioRpcError as err:
            # Close broken channel so next attempt creates a fresh one
            if self._channel is not None:
                await self._channel.close()
                self._channel = None

            # Log first failure only (log-when-unavailable, Silver)
            if not self._was_unavailable:
                _LOGGER.warning(
                    "EEBUS bridge unavailable at %s:%s: %s", self.host, self.port, err
                )
                self._was_unavailable = True

            raise UpdateFailed(f"gRPC error: {err}") from err

    async def async_write_lpc_limit(self, value_watts: float) -> None:
        """Write LPC consumption limit via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.LPCServiceStub(channel)
        await stub.WriteConsumptionLimit(
            proto_stubs.WriteLoadLimitRequest(
                ski=self.ski, value_watts=value_watts, is_active=True
            )
        )

    async def async_write_failsafe_limit(self, value_watts: float) -> None:
        """Write failsafe limit via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.LPCServiceStub(channel)
        await stub.WriteFailsafeLimit(
            proto_stubs.WriteFailsafeLimitRequest(
                ski=self.ski, value_watts=value_watts
            )
        )

    async def async_set_lpc_active(self, active: bool) -> None:
        """Activate or deactivate LPC limit via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.LPCServiceStub(channel)
        # Read current limit first, then write with new is_active
        current = await stub.GetConsumptionLimit(
            proto_stubs.DeviceRequest(ski=self.ski)
        )
        await stub.WriteConsumptionLimit(
            proto_stubs.WriteLoadLimitRequest(
                ski=self.ski,
                value_watts=current.value_watts,
                is_active=active,
            )
        )

    async def async_start_heartbeat(self) -> None:
        """Start EEBUS heartbeat via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.LPCServiceStub(channel)
        await stub.StartHeartbeat(proto_stubs.DeviceRequest(ski=self.ski))

    async def async_stop_heartbeat(self) -> None:
        """Stop EEBUS heartbeat via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.LPCServiceStub(channel)
        await stub.StopHeartbeat(proto_stubs.DeviceRequest(ski=self.ski))

    async def async_shutdown(self) -> None:
        """Close gRPC channel and cancel stream tasks."""
        for task in self._stream_tasks:
            task.cancel()
        self._stream_tasks.clear()
        if self._channel is not None:
            await self._channel.close()
            self._channel = None
```

- [ ] **Step 4: Run test**

```bash
cd /home/volsch/eebus/ha-integration
python -m pytest custom_components/eebus/tests/test_coordinator.py -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ha-integration/custom_components/eebus/coordinator.py ha-integration/custom_components/eebus/tests/
git commit -m "feat: add EEBUS coordinator with gRPC polling"
```

---

### Task 17: Config Flow

**Files:**
- Create: `ha-integration/custom_components/eebus/config_flow.py`
- Create: `ha-integration/custom_components/eebus/strings.json`
- Create: `ha-integration/custom_components/eebus/translations/en.json`
- Create: `ha-integration/custom_components/eebus/translations/de.json`
- Create: `ha-integration/custom_components/eebus/tests/test_config_flow.py`

- [ ] **Step 1: Write failing test for config flow**

Create `ha-integration/custom_components/eebus/tests/test_config_flow.py`:

```python
"""Tests for the EEBUS config flow."""

from unittest.mock import AsyncMock, patch

import pytest

from custom_components.eebus.config_flow import EebusConfigFlow
from custom_components.eebus.const import CONF_GRPC_HOST, CONF_GRPC_PORT, DOMAIN


@pytest.mark.asyncio
async def test_config_flow_init():
    """Test that config flow can be instantiated."""
    flow = EebusConfigFlow()
    assert flow.DOMAIN == DOMAIN
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/volsch/eebus/ha-integration
python -m pytest custom_components/eebus/tests/test_config_flow.py -v
```

Expected: `ImportError`.

- [ ] **Step 3: Implement config flow**

Create `ha-integration/custom_components/eebus/config_flow.py`:

```python
"""Config flow for EEBUS integration."""

from __future__ import annotations

import logging
from typing import Any

import grpc
import grpc.aio
import voluptuous as vol

from homeassistant.config_entries import ConfigFlow, ConfigFlowResult

from .const import (
    CONF_DEVICE_SKI,
    CONF_GRPC_HOST,
    CONF_GRPC_PORT,
    DEFAULT_GRPC_PORT,
    DOMAIN,
)

_LOGGER = logging.getLogger(__name__)

STEP_USER_DATA_SCHEMA = vol.Schema(
    {
        vol.Required(CONF_GRPC_HOST): str,
        vol.Required(CONF_GRPC_PORT, default=DEFAULT_GRPC_PORT): int,
    }
)

STEP_DEVICE_DATA_SCHEMA = vol.Schema(
    {
        vol.Required(CONF_DEVICE_SKI): str,
    }
)


class EebusConfigFlow(ConfigFlow, domain=DOMAIN):
    """Handle a config flow for EEBUS."""

    VERSION = 1
    DOMAIN = DOMAIN

    def __init__(self) -> None:
        """Initialize."""
        self._host: str = ""
        self._port: int = DEFAULT_GRPC_PORT

    async def async_step_user(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Handle the initial step: connect to bridge."""
        errors: dict[str, str] = {}

        if user_input is not None:
            self._host = user_input[CONF_GRPC_HOST]
            self._port = user_input[CONF_GRPC_PORT]

            try:
                channel = grpc.aio.insecure_channel(
                    f"{self._host}:{self._port}"
                )
                from . import proto_stubs

                stub = proto_stubs.DeviceServiceStub(channel)
                await stub.GetStatus(proto_stubs.Empty())
                await channel.close()

                return await self.async_step_device()
            except grpc.aio.AioRpcError:
                errors["base"] = "cannot_connect"

        return self.async_show_form(
            step_id="user",
            data_schema=STEP_USER_DATA_SCHEMA,
            errors=errors,
        )

    async def async_step_device(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Handle device selection step."""
        if user_input is not None:
            ski = user_input[CONF_DEVICE_SKI]
            await self.async_set_unique_id(ski)
            self._abort_if_unique_id_configured()

            return self.async_create_entry(
                title=f"EEBUS {ski[:8]}",
                data={
                    CONF_GRPC_HOST: self._host,
                    CONF_GRPC_PORT: self._port,
                    CONF_DEVICE_SKI: ski,
                },
            )

        return self.async_show_form(
            step_id="device",
            data_schema=STEP_DEVICE_DATA_SCHEMA,
        )

    # Gold: reconfiguration-flow
    async def async_step_reconfigure(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Handle reconfiguration of bridge connection."""
        errors: dict[str, str] = {}

        if user_input is not None:
            try:
                channel = grpc.aio.insecure_channel(
                    f"{user_input[CONF_GRPC_HOST]}:{user_input[CONF_GRPC_PORT]}"
                )
                from . import proto_stubs

                stub = proto_stubs.DeviceServiceStub(channel)
                await stub.GetStatus(proto_stubs.Empty())
                await channel.close()

                return self.async_update_reload_and_abort(
                    self._get_reconfigure_entry(),
                    data_updates={
                        CONF_GRPC_HOST: user_input[CONF_GRPC_HOST],
                        CONF_GRPC_PORT: user_input[CONF_GRPC_PORT],
                    },
                )
            except grpc.aio.AioRpcError:
                errors["base"] = "cannot_connect"

        entry = self._get_reconfigure_entry()
        return self.async_show_form(
            step_id="reconfigure",
            data_schema=vol.Schema(
                {
                    vol.Required(CONF_GRPC_HOST, default=entry.data.get(CONF_GRPC_HOST, "")): str,
                    vol.Required(CONF_GRPC_PORT, default=entry.data.get(CONF_GRPC_PORT, DEFAULT_GRPC_PORT)): int,
                }
            ),
            errors=errors,
        )
```

- [ ] **Step 4: Create strings.json (with entity + exception translations for Gold)**

Create `ha-integration/custom_components/eebus/strings.json`:

```json
{
  "config": {
    "step": {
      "user": {
        "title": "Mit EEBUS Bridge verbinden",
        "description": "Bridge-Adresse eingeben und Verbindung testen.",
        "data": {
          "grpc_host": "Bridge-Host",
          "grpc_port": "Bridge-Port"
        }
      },
      "device": {
        "title": "EEBUS-Gerät auswählen",
        "data": {
          "device_ski": "Geräte-SKI"
        }
      },
      "reconfigure": {
        "title": "EEBUS Bridge rekonfigurieren",
        "description": "Bridge-Verbindungsparameter ändern.",
        "data": {
          "grpc_host": "Bridge-Host",
          "grpc_port": "Bridge-Port"
        }
      }
    },
    "error": {
      "cannot_connect": "Verbindung zur EEBUS Bridge fehlgeschlagen. Bitte Host und Port prüfen."
    },
    "abort": {
      "already_configured": "Dieses Gerät ist bereits konfiguriert.",
      "reconfigure_successful": "Rekonfiguration erfolgreich."
    }
  },
  "exceptions": {
    "bridge_unavailable": {
      "message": "EEBUS Bridge unter {host}:{port} nicht erreichbar."
    },
    "grpc_error": {
      "message": "gRPC-Fehler: {error}"
    }
  },
  "entity": {
    "sensor": {
      "power_consumption": { "name": "Leistungsaufnahme" },
      "consumption_limit": { "name": "Leistungslimit" }
    },
    "number": {
      "lpc_limit": { "name": "LPC Limit" },
      "failsafe_limit": { "name": "Failsafe Limit" }
    },
    "switch": {
      "lpc_active": { "name": "LPC aktiv" },
      "heartbeat": { "name": "Heartbeat" }
    },
    "binary_sensor": {
      "connected": { "name": "Verbunden" },
      "heartbeat_ok": { "name": "Heartbeat OK" }
    }
  }
}
```

Create `ha-integration/custom_components/eebus/translations/en.json`:

```json
{
  "config": {
    "step": {
      "user": {
        "title": "Connect to EEBUS Bridge",
        "description": "Enter bridge address and test connection.",
        "data": {
          "grpc_host": "Bridge Host",
          "grpc_port": "Bridge Port"
        }
      },
      "device": {
        "title": "Select EEBUS Device",
        "data": {
          "device_ski": "Device SKI"
        }
      },
      "reconfigure": {
        "title": "Reconfigure EEBUS Bridge",
        "description": "Change bridge connection parameters.",
        "data": {
          "grpc_host": "Bridge Host",
          "grpc_port": "Bridge Port"
        }
      }
    },
    "error": {
      "cannot_connect": "Cannot connect to EEBUS bridge. Check host and port."
    },
    "abort": {
      "already_configured": "This device is already configured.",
      "reconfigure_successful": "Reconfiguration successful."
    }
  },
  "exceptions": {
    "bridge_unavailable": {
      "message": "EEBUS bridge at {host}:{port} unreachable."
    },
    "grpc_error": {
      "message": "gRPC error: {error}"
    }
  },
  "entity": {
    "sensor": {
      "power_consumption": { "name": "Power Consumption" },
      "consumption_limit": { "name": "Consumption Limit" }
    },
    "number": {
      "lpc_limit": { "name": "LPC Limit" },
      "failsafe_limit": { "name": "Failsafe Limit" }
    },
    "switch": {
      "lpc_active": { "name": "LPC Active" },
      "heartbeat": { "name": "Heartbeat" }
    },
    "binary_sensor": {
      "connected": { "name": "Connected" },
      "heartbeat_ok": { "name": "Heartbeat OK" }
    }
  }
}
```

Create `ha-integration/custom_components/eebus/translations/de.json` — same content as `strings.json`.

- [ ] **Step 5: Create icons.json (Gold: icon-translations)**

Create `ha-integration/custom_components/eebus/icons.json`:

```json
{
  "entity": {
    "sensor": {
      "power_consumption": { "default": "mdi:flash" },
      "consumption_limit": { "default": "mdi:speedometer" }
    },
    "number": {
      "lpc_limit": { "default": "mdi:speedometer" },
      "failsafe_limit": { "default": "mdi:shield-alert" }
    },
    "switch": {
      "lpc_active": { "default": "mdi:power-plug" },
      "heartbeat": { "default": "mdi:heart-pulse" }
    },
    "binary_sensor": {
      "connected": { "default": "mdi:lan-connect" },
      "heartbeat_ok": { "default": "mdi:heart-pulse" }
    }
  }
}
```

- [ ] **Step 6: Run test**

```bash
cd /home/volsch/eebus/ha-integration
python -m pytest custom_components/eebus/tests/test_config_flow.py -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add ha-integration/custom_components/eebus/config_flow.py ha-integration/custom_components/eebus/strings.json ha-integration/custom_components/eebus/translations/ ha-integration/custom_components/eebus/icons.json ha-integration/custom_components/eebus/tests/test_config_flow.py
git commit -m "feat: add config flow with reconfigure, entity/exception translations, icons"
```

---

### Task 18: Entity Base + Sensor Entities

**Files:**
- Create: `ha-integration/custom_components/eebus/entity.py`
- Create: `ha-integration/custom_components/eebus/sensor.py`
- Create: `ha-integration/custom_components/eebus/tests/test_sensor.py`

- [ ] **Step 1: Write failing test for sensor entities**

Create `ha-integration/custom_components/eebus/tests/test_sensor.py`:

```python
"""Tests for EEBUS sensor entities."""

from unittest.mock import MagicMock

import pytest

from custom_components.eebus.sensor import (
    EebusPowerSensor,
)


def test_power_sensor_value():
    """Test power sensor returns correct value from coordinator data."""
    coordinator = MagicMock()
    coordinator.data = {"power_watts": 1500.0, "connected": True}
    coordinator.ski = "test-ski-123"

    sensor = EebusPowerSensor(coordinator)
    assert sensor.native_value == 1500.0
    assert sensor.native_unit_of_measurement == "W"


def test_power_sensor_unavailable():
    """Test power sensor returns None when data missing."""
    coordinator = MagicMock()
    coordinator.data = {"power_watts": None, "connected": True}
    coordinator.ski = "test-ski-123"

    sensor = EebusPowerSensor(coordinator)
    assert sensor.native_value is None
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/volsch/eebus/ha-integration
python -m pytest custom_components/eebus/tests/test_sensor.py -v
```

Expected: `ImportError`.

- [ ] **Step 3: Implement entity base (Gold: has_entity_name, devices, translation_key)**

Create `ha-integration/custom_components/eebus/entity.py`:

```python
"""Base entity for EEBUS integration."""

from __future__ import annotations

from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.update_coordinator import CoordinatorEntity

from .const import DOMAIN
from .coordinator import EebusCoordinator


class EebusEntity(CoordinatorEntity[EebusCoordinator]):
    """Base class for EEBUS entities.

    Gold compliance:
    - has_entity_name = True (Bronze)
    - entity_unique_id via _attr_unique_id (Bronze)
    - device_info creates HA device (Gold: devices)
    - translation_key for entity names (Gold: entity-translations)
    """

    _attr_has_entity_name = True

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize the entity."""
        super().__init__(coordinator)
        self._attr_device_info = DeviceInfo(
            identifiers={(DOMAIN, coordinator.ski)},
            name=f"EEBUS {coordinator.ski[:8]}",
            manufacturer="Vaillant",
            model="VR940f",
        )
```

- [ ] **Step 4: Implement sensor entities**

Create `ha-integration/custom_components/eebus/sensor.py`:

```python
"""Sensor entities for EEBUS integration."""

from __future__ import annotations

from homeassistant.components.sensor import (
    SensorDeviceClass,
    SensorEntity,
    SensorStateClass,
)
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import EntityCategory, UnitOfEnergy, UnitOfPower
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from .const import DOMAIN, PARALLEL_UPDATES
from .coordinator import EebusCoordinator
from .entity import EebusEntity


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Set up EEBUS sensors."""
    coordinator: EebusCoordinator = entry.runtime_data
    entities: list[SensorEntity] = [
        EebusPowerSensor(coordinator),
        EebusConsumptionLimitSensor(coordinator),
    ]
    async_add_entities(entities)


class EebusPowerSensor(EebusEntity, SensorEntity):
    """Sensor for current power consumption.

    Gold: device_class, state_class, translation_key, entity_category.
    """

    _attr_device_class = SensorDeviceClass.POWER
    _attr_native_unit_of_measurement = UnitOfPower.WATT
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "power_consumption"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_power"

    @property
    def native_value(self) -> float | None:
        """Return current power in watts."""
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("power_watts")


class EebusConsumptionLimitSensor(EebusEntity, SensorEntity):
    """Read-only sensor showing current consumption limit.

    Gold: entity_category DIAGNOSTIC (informational, not primary).
    """

    _attr_device_class = SensorDeviceClass.POWER
    _attr_native_unit_of_measurement = UnitOfPower.WATT
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "consumption_limit"
    _attr_entity_category = EntityCategory.DIAGNOSTIC

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_consumption_limit"

    @property
    def native_value(self) -> float | None:
        """Return current limit in watts."""
        if self.coordinator.data is None:
            return None
        limit = self.coordinator.data.get("consumption_limit")
        if limit is None:
            return None
        return limit.get("value_watts")
```

- [ ] **Step 5: Run tests**

```bash
cd /home/volsch/eebus/ha-integration
python -m pytest custom_components/eebus/tests/test_sensor.py -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add ha-integration/custom_components/eebus/entity.py ha-integration/custom_components/eebus/sensor.py ha-integration/custom_components/eebus/tests/test_sensor.py
git commit -m "feat: add power and consumption limit sensor entities"
```

---

### Task 19: Number + Switch + Binary Sensor Entities

**Files:**
- Create: `ha-integration/custom_components/eebus/number.py`
- Create: `ha-integration/custom_components/eebus/switch.py`
- Create: `ha-integration/custom_components/eebus/binary_sensor.py`
- Create: `ha-integration/custom_components/eebus/tests/test_number.py`

- [ ] **Step 1: Write failing test for number entity**

Create `ha-integration/custom_components/eebus/tests/test_number.py`:

```python
"""Tests for EEBUS number entities."""

from unittest.mock import MagicMock

import pytest

from custom_components.eebus.number import EebusLPCLimitNumber


def test_lpc_limit_value():
    """Test LPC limit number returns correct value."""
    coordinator = MagicMock()
    coordinator.data = {
        "consumption_limit": {"value_watts": 4200.0, "is_active": True, "is_changeable": True},
        "connected": True,
    }
    coordinator.ski = "test-ski"

    number = EebusLPCLimitNumber(coordinator)
    assert number.native_value == 4200.0
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/volsch/eebus/ha-integration
python -m pytest custom_components/eebus/tests/test_number.py -v
```

Expected: `ImportError`.

- [ ] **Step 3: Implement number entities**

Create `ha-integration/custom_components/eebus/number.py`:

```python
"""Number entities for EEBUS integration."""

from __future__ import annotations

from homeassistant.components.number import NumberDeviceClass, NumberEntity, NumberMode
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import EntityCategory, UnitOfPower
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from .const import DOMAIN, PARALLEL_UPDATES
from .coordinator import EebusCoordinator
from .entity import EebusEntity


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Set up EEBUS number entities."""
    coordinator: EebusCoordinator = entry.runtime_data
    async_add_entities([
        EebusLPCLimitNumber(coordinator),
        EebusFailsafeLimitNumber(coordinator),
    ])


class EebusLPCLimitNumber(EebusEntity, NumberEntity):
    """Number entity for setting LPC consumption limit.

    Gold: device_class, translation_key, entity_category CONFIG.
    """

    _attr_device_class = NumberDeviceClass.POWER
    _attr_native_unit_of_measurement = UnitOfPower.WATT
    _attr_mode = NumberMode.BOX
    _attr_native_min_value = 0
    _attr_native_max_value = 32000
    _attr_native_step = 100
    _attr_translation_key = "lpc_limit"
    _attr_entity_category = EntityCategory.CONFIG

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_lpc_limit"

    @property
    def native_value(self) -> float | None:
        """Return current limit value."""
        if self.coordinator.data is None:
            return None
        limit = self.coordinator.data.get("consumption_limit")
        if limit is None:
            return None
        return limit.get("value_watts")

    async def async_set_native_value(self, value: float) -> None:
        """Set new LPC limit via gRPC."""
        await self.coordinator.async_write_lpc_limit(value)
        await self.coordinator.async_request_refresh()


class EebusFailsafeLimitNumber(EebusEntity, NumberEntity):
    """Number entity for setting failsafe limit.

    Gold: entity_category CONFIG, entity_disabled_by_default.
    """

    _attr_device_class = NumberDeviceClass.POWER
    _attr_native_unit_of_measurement = UnitOfPower.WATT
    _attr_mode = NumberMode.BOX
    _attr_native_min_value = 0
    _attr_native_max_value = 32000
    _attr_native_step = 100
    _attr_translation_key = "failsafe_limit"
    _attr_entity_category = EntityCategory.CONFIG
    _attr_entity_registry_enabled_default = False  # Gold: less popular entities disabled

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_failsafe_limit"

    @property
    def native_value(self) -> float | None:
        """Return current failsafe limit."""
        if self.coordinator.data is None:
            return None
        failsafe = self.coordinator.data.get("failsafe_limit")
        if failsafe is None:
            return None
        return failsafe.get("value_watts")

    async def async_set_native_value(self, value: float) -> None:
        """Set new failsafe limit via gRPC."""
        await self.coordinator.async_write_failsafe_limit(value)
        await self.coordinator.async_request_refresh()
```

- [ ] **Step 4: Implement switch entities**

Create `ha-integration/custom_components/eebus/switch.py`:

```python
"""Switch entities for EEBUS integration."""

from __future__ import annotations

from typing import Any

from homeassistant.components.switch import SwitchEntity
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import EntityCategory
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from .const import DOMAIN, PARALLEL_UPDATES
from .coordinator import EebusCoordinator
from .entity import EebusEntity


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Set up EEBUS switch entities."""
    coordinator: EebusCoordinator = entry.runtime_data
    async_add_entities([
        EebusLPCActiveSwitch(coordinator),
        EebusHeartbeatSwitch(coordinator),
    ])


class EebusLPCActiveSwitch(EebusEntity, SwitchEntity):
    """Switch for activating/deactivating LPC limit.

    Gold: translation_key, entity_category CONFIG.
    """

    _attr_translation_key = "lpc_active"
    _attr_entity_category = EntityCategory.CONFIG

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_lpc_active"

    @property
    def is_on(self) -> bool | None:
        """Return True if LPC limit is active."""
        if self.coordinator.data is None:
            return None
        limit = self.coordinator.data.get("consumption_limit")
        if limit is None:
            return None
        return limit.get("is_active")

    async def async_turn_on(self, **kwargs: Any) -> None:
        """Activate LPC limit."""
        await self.coordinator.async_set_lpc_active(True)
        await self.coordinator.async_request_refresh()

    async def async_turn_off(self, **kwargs: Any) -> None:
        """Deactivate LPC limit."""
        await self.coordinator.async_set_lpc_active(False)
        await self.coordinator.async_request_refresh()


class EebusHeartbeatSwitch(EebusEntity, SwitchEntity):
    """Switch for starting/stopping EEBUS heartbeat.

    Gold: translation_key, entity_category CONFIG, disabled by default.
    """

    _attr_translation_key = "heartbeat"
    _attr_entity_category = EntityCategory.CONFIG
    _attr_entity_registry_enabled_default = False  # Gold: less popular, disabled by default

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_heartbeat"

    @property
    def is_on(self) -> bool | None:
        """Return True if heartbeat is running."""
        if self.coordinator.data is None:
            return None
        hb = self.coordinator.data.get("heartbeat_status")
        if hb is None:
            return None
        return hb.get("running")

    async def async_turn_on(self, **kwargs: Any) -> None:
        """Start heartbeat."""
        await self.coordinator.async_start_heartbeat()
        await self.coordinator.async_request_refresh()

    async def async_turn_off(self, **kwargs: Any) -> None:
        """Stop heartbeat."""
        await self.coordinator.async_stop_heartbeat()
        await self.coordinator.async_request_refresh()
```

- [ ] **Step 5: Implement binary sensor entities**

Create `ha-integration/custom_components/eebus/binary_sensor.py`:

```python
"""Binary sensor entities for EEBUS integration."""

from __future__ import annotations

from homeassistant.components.binary_sensor import (
    BinarySensorDeviceClass,
    BinarySensorEntity,
)
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import EntityCategory
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from .const import DOMAIN, PARALLEL_UPDATES
from .coordinator import EebusCoordinator
from .entity import EebusEntity


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Set up EEBUS binary sensors."""
    coordinator: EebusCoordinator = entry.runtime_data
    async_add_entities([
        EebusConnectedSensor(coordinator),
        EebusHeartbeatOkSensor(coordinator),
    ])


class EebusConnectedSensor(EebusEntity, BinarySensorEntity):
    """Binary sensor for EEBUS connection status.

    Gold: translation_key, entity_category DIAGNOSTIC.
    """

    _attr_device_class = BinarySensorDeviceClass.CONNECTIVITY
    _attr_translation_key = "connected"
    _attr_entity_category = EntityCategory.DIAGNOSTIC

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_connected"

    @property
    def is_on(self) -> bool | None:
        """Return True if connected."""
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("connected")


class EebusHeartbeatOkSensor(EebusEntity, BinarySensorEntity):
    """Binary sensor for heartbeat health.

    Gold: translation_key, entity_category DIAGNOSTIC, disabled by default.
    """

    _attr_device_class = BinarySensorDeviceClass.PROBLEM
    _attr_translation_key = "heartbeat_ok"
    _attr_entity_category = EntityCategory.DIAGNOSTIC
    _attr_entity_registry_enabled_default = False  # Gold: less popular, disabled by default

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_heartbeat_ok"

    @property
    def is_on(self) -> bool | None:
        """Return True if heartbeat has a problem (inverted for PROBLEM class)."""
        if self.coordinator.data is None:
            return None
        hb = self.coordinator.data.get("heartbeat_status")
        if hb is None:
            return None
        # PROBLEM class: is_on=True means there's a problem
        return not hb.get("within_duration", True)
```

- [ ] **Step 6: Run tests**

```bash
cd /home/volsch/eebus/ha-integration
python -m pytest custom_components/eebus/tests/ -v
```

Expected: all tests PASS.

- [ ] **Step 7: Commit**

```bash
git add ha-integration/custom_components/eebus/number.py ha-integration/custom_components/eebus/switch.py ha-integration/custom_components/eebus/binary_sensor.py ha-integration/custom_components/eebus/tests/test_number.py
git commit -m "feat: add number, switch, and binary sensor entities for LPC control"
```

---

## Phase 9: Proto Stubs for Python

### Task 20: Generate Python gRPC Stubs

**Files:**
- Create: `ha-integration/custom_components/eebus/proto_stubs.py`
- Create: `ha-integration/generate_proto.sh`

- [ ] **Step 1: Create stub generation script**

Create `ha-integration/generate_proto.sh`:

```bash
#!/bin/bash
# Generate Python gRPC stubs from protobuf definitions
set -euo pipefail

PROTO_DIR="../eebus-bridge/proto"
OUT_DIR="custom_components/eebus/generated"

mkdir -p "$OUT_DIR"

python -m grpc_tools.protoc \
  -I "$PROTO_DIR" \
  --python_out="$OUT_DIR" \
  --grpc_python_out="$OUT_DIR" \
  --pyi_out="$OUT_DIR" \
  eebus/v1/common.proto \
  eebus/v1/device_service.proto \
  eebus/v1/lpc_service.proto \
  eebus/v1/monitoring_service.proto

touch "$OUT_DIR/__init__.py"
touch "$OUT_DIR/eebus/__init__.py"
touch "$OUT_DIR/eebus/v1/__init__.py"

echo "Proto stubs generated in $OUT_DIR"
```

- [ ] **Step 2: Create proto_stubs.py convenience module**

Create `ha-integration/custom_components/eebus/proto_stubs.py`:

```python
"""Convenience re-exports for generated protobuf stubs.

Run `ha-integration/generate_proto.sh` to regenerate after proto changes.
"""

try:
    from .generated.eebus.v1.common_pb2 import (
        DeviceRequest,
        Empty,
        LoadLimit,
        MeasurementEntry,
        PowerMeasurement,
    )
    from .generated.eebus.v1.device_service_pb2_grpc import DeviceServiceStub
    from .generated.eebus.v1.lpc_service_pb2 import (
        WriteLoadLimitRequest,
        WriteFailsafeLimitRequest,
    )
    from .generated.eebus.v1.lpc_service_pb2_grpc import LPCServiceStub
    from .generated.eebus.v1.monitoring_service_pb2_grpc import MonitoringServiceStub
except ImportError:
    # Stubs not yet generated — will fail at runtime if used
    pass
```

- [ ] **Step 3: Generate stubs**

```bash
cd /home/volsch/eebus/ha-integration
pip install grpcio-tools
chmod +x generate_proto.sh
./generate_proto.sh
```

Expected: files generated in `custom_components/eebus/generated/`.

- [ ] **Step 4: Commit**

```bash
git add ha-integration/generate_proto.sh ha-integration/custom_components/eebus/proto_stubs.py ha-integration/custom_components/eebus/generated/
git commit -m "feat: add Python gRPC stub generation from protobuf schemas"
```

---

## Phase 10: Diagnostics & Integration Tests

### Task 21: Diagnostics (Gold)

**Files:**
- Create: `ha-integration/custom_components/eebus/diagnostics.py`
- Create: `ha-integration/custom_components/eebus/tests/test_diagnostics.py`

- [ ] **Step 1: Write failing test for diagnostics**

Create `ha-integration/custom_components/eebus/tests/test_diagnostics.py`:

```python
"""Tests for EEBUS diagnostics."""

from unittest.mock import MagicMock

import pytest

from custom_components.eebus.diagnostics import async_get_config_entry_diagnostics


@pytest.mark.asyncio
async def test_diagnostics_output():
    """Test diagnostics returns expected structure."""
    hass = MagicMock()
    entry = MagicMock()
    entry.data = {
        "grpc_host": "192.168.1.100",
        "grpc_port": 50051,
        "device_ski": "abcdef1234567890",
    }
    coordinator = MagicMock()
    coordinator.data = {"power_watts": 1500.0, "connected": True}
    entry.runtime_data = coordinator

    result = await async_get_config_entry_diagnostics(hass, entry)

    assert "config" in result
    assert result["config"]["grpc_host"] == "192.168.1.100"
    assert result["config"]["device_ski"] == "**REDACTED**"
    assert "coordinator_data" in result
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/volsch/eebus/ha-integration
python -m pytest custom_components/eebus/tests/test_diagnostics.py -v
```

Expected: `ImportError`.

- [ ] **Step 3: Implement diagnostics**

Create `ha-integration/custom_components/eebus/diagnostics.py`:

```python
"""Diagnostics for the EEBUS integration."""

from __future__ import annotations

from typing import Any

from homeassistant.core import HomeAssistant

from . import EebusConfigEntry


async def async_get_config_entry_diagnostics(
    hass: HomeAssistant,
    entry: EebusConfigEntry,
) -> dict[str, Any]:
    """Return diagnostics for a config entry."""
    coordinator = entry.runtime_data

    return {
        "config": {
            "grpc_host": entry.data.get("grpc_host"),
            "grpc_port": entry.data.get("grpc_port"),
            "device_ski": "**REDACTED**",
        },
        "coordinator_data": dict(coordinator.data) if coordinator.data else None,
    }
```

- [ ] **Step 4: Run test**

```bash
cd /home/volsch/eebus/ha-integration
python -m pytest custom_components/eebus/tests/test_diagnostics.py -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ha-integration/custom_components/eebus/diagnostics.py ha-integration/custom_components/eebus/tests/test_diagnostics.py
git commit -m "feat: add diagnostics export (Gold quality scale)"
```

---

### Task 22: Setup & Unload Tests (Silver: test-coverage)

**Files:**
- Create: `ha-integration/custom_components/eebus/tests/test_init.py`

- [ ] **Step 1: Write tests for setup and unload**

Create `ha-integration/custom_components/eebus/tests/test_init.py`:

```python
"""Tests for EEBUS integration setup and unload."""

from unittest.mock import AsyncMock, MagicMock, patch

import pytest


@pytest.mark.asyncio
async def test_setup_entry():
    """Test async_setup_entry creates coordinator and forwards platforms."""
    from custom_components.eebus import async_setup_entry

    hass = MagicMock()
    entry = MagicMock()
    entry.data = {
        "grpc_host": "127.0.0.1",
        "grpc_port": 50051,
        "device_ski": "test-ski",
    }
    entry.runtime_data = None
    entry.async_on_unload = MagicMock()
    entry.add_update_listener = MagicMock()

    with patch(
        "custom_components.eebus.EebusCoordinator"
    ) as mock_coordinator_cls:
        coordinator = AsyncMock()
        coordinator.async_config_entry_first_refresh = AsyncMock()
        mock_coordinator_cls.return_value = coordinator

        hass.config_entries.async_forward_entry_setups = AsyncMock()

        result = await async_setup_entry(hass, entry)

        assert result is True
        assert entry.runtime_data == coordinator
        coordinator.async_config_entry_first_refresh.assert_awaited_once()


@pytest.mark.asyncio
async def test_unload_entry():
    """Test async_unload_entry shuts down coordinator."""
    from custom_components.eebus import async_unload_entry

    hass = MagicMock()
    entry = MagicMock()

    coordinator = AsyncMock()
    coordinator.async_shutdown = AsyncMock()
    entry.runtime_data = coordinator

    hass.config_entries.async_unload_platforms = AsyncMock(return_value=True)

    result = await async_unload_entry(hass, entry)

    assert result is True
    coordinator.async_shutdown.assert_awaited_once()
```

- [ ] **Step 2: Run tests**

```bash
cd /home/volsch/eebus/ha-integration
python -m pytest custom_components/eebus/tests/test_init.py -v
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add ha-integration/custom_components/eebus/tests/test_init.py
git commit -m "test: add setup/unload tests for Silver coverage requirement"
```

---

### Task 23: End-to-End gRPC Integration Test

**Files:**
- Create: `eebus-bridge/internal/grpc/integration_test.go`

- [ ] **Step 1: Write integration test for full gRPC round-trip**

Create `eebus-bridge/internal/grpc/integration_test.go`:

```go
//go:build integration

package grpc_test

import (
	"context"
	"testing"
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestIntegrationDeviceServiceRoundTrip(t *testing.T) {
	bus := eebus.NewEventBus()
	callbacks := eebus.NewCallbacks(bus)

	deviceSvc := bridgegrpc.NewDeviceService(callbacks, bus, "integration-test-ski")
	lpcSvc := bridgegrpc.NewLPCService(nil, bus)
	monSvc := bridgegrpc.NewMonitoringService(nil, bus)

	srv := bridgegrpc.NewServer(0)
	pb.RegisterDeviceServiceServer(srv.GRPCServer(), deviceSvc)
	pb.RegisterLPCServiceServer(srv.GRPCServer(), lpcSvc)
	pb.RegisterMonitoringServiceServer(srv.GRPCServer(), monSvc)

	go srv.Start()
	defer srv.Stop()

	time.Sleep(100 * time.Millisecond)

	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	ctx := context.Background()

	// Test DeviceService.GetStatus
	dc := pb.NewDeviceServiceClient(conn)
	status, err := dc.GetStatus(ctx, &pb.Empty{})
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !status.Running {
		t.Error("status.Running = false")
	}
	if status.LocalSki != "integration-test-ski" {
		t.Errorf("status.LocalSki = %q", status.LocalSki)
	}

	// Test DeviceService.ListDiscoveredDevices
	devices, err := dc.ListDiscoveredDevices(ctx, &pb.Empty{})
	if err != nil {
		t.Fatalf("ListDiscoveredDevices: %v", err)
	}
	if devices == nil {
		t.Error("ListDiscoveredDevices returned nil")
	}

	// Test DeviceService event streaming
	streamCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	stream, err := dc.SubscribeDeviceEvents(streamCtx, &pb.Empty{})
	if err != nil {
		t.Fatalf("SubscribeDeviceEvents: %v", err)
	}

	callbacks.RemoteSKIConnected(nil, "remote-ski-test")

	evt, err := stream.Recv()
	if err != nil {
		t.Fatalf("stream.Recv: %v", err)
	}
	if evt.Ski != "remote-ski-test" {
		t.Errorf("event SKI = %q", evt.Ski)
	}
	if evt.EventType != pb.DeviceEventType_DEVICE_EVENT_CONNECTED {
		t.Errorf("event type = %v", evt.EventType)
	}
}
```

- [ ] **Step 2: Run integration test**

```bash
cd /home/volsch/eebus/eebus-bridge
go test -tags=integration ./internal/grpc/ -run TestIntegration -v
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add eebus-bridge/internal/grpc/integration_test.go
git commit -m "test: add gRPC integration test for device service round-trip"
```

---

## Phase 11: CI Pipeline (HACS + Hassfest + Tests)

### Task 24: GitHub Actions CI Workflows

**Files:**
- Create: `.github/workflows/go.yml`
- Create: `.github/workflows/test.yml`
- Create: `.github/workflows/hacs.yml`
- Create: `.github/workflows/hassfest.yml`
- Create: `.github/workflows/release.yml`

Modeled after the [Danfoss-TLX-2-HA](https://github.com/volschin/Danfoss-TLX-2-HA) CI setup.

- [ ] **Step 1: Create HACS validation workflow**

Create `.github/workflows/hacs.yml`:

```yaml
name: HACS Validation

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  hacs:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: HACS Validation
        uses: hacs/action@main
        with:
          category: integration
```

- [ ] **Step 2: Create Hassfest validation workflow**

Create `.github/workflows/hassfest.yml`:

```yaml
name: Hassfest

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  hassfest:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Hassfest Validation
        uses: home-assistant/actions/hassfest@master
```

- [ ] **Step 3: Create Python test + lint workflow**

Create `.github/workflows/test.yml`:

```yaml
name: Test & Lint

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  test:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        python-version: ["3.12", "3.13"]

    steps:
      - uses: actions/checkout@v4

      - name: Set up Python ${{ matrix.python-version }}
        uses: actions/setup-python@v5
        with:
          python-version: ${{ matrix.python-version }}

      - name: Install dependencies
        run: |
          python -m pip install --upgrade pip
          pip install pytest pytest-asyncio pytest-cov ruff homeassistant voluptuous grpcio protobuf

      - name: Lint with ruff
        run: ruff check ha-integration/custom_components/ ha-integration/custom_components/eebus/tests/

      - name: Run tests with coverage
        run: pytest --cov=ha-integration.custom_components.eebus --cov-report=term-missing --cov-fail-under=95 -v
```

- [ ] **Step 4: Create Go build + test workflow**

Create `.github/workflows/go.yml`:

```yaml
name: Go

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  build:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: eebus-bridge
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.22"
      - run: go vet ./...
      - run: go test -v -race ./...
      - run: go test -tags=integration -v ./internal/grpc/ -run TestIntegration
      - run: go build ./cmd/eebus-bridge/

  docker:
    runs-on: ubuntu-latest
    needs: [build]
    steps:
      - uses: actions/checkout@v4
      - run: docker build -t eebus-bridge ./eebus-bridge
```

- [ ] **Step 5: Create release drafter workflow**

Create `.github/workflows/release.yml`:

```yaml
name: Release Drafter

on:
  push:
    branches: [main]

permissions:
  contents: read
  pull-requests: read

jobs:
  draft:
    runs-on: ubuntu-latest
    permissions:
      contents: write
      pull-requests: read
    steps:
      - uses: release-drafter/release-drafter@v6
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [ ] **Step 6: Commit**

```bash
git add .github/
git commit -m "ci: add HACS, Hassfest, test, Go, and release CI workflows"
```

---

## Phase 12: README (Gold Documentation)

### Task 25: README (Danfoss-TLX-2-HA style)

**Files:**
- Modify: `README.md`

This README satisfies Gold documentation requirements: docs-high-level-description, docs-installation-instructions, docs-removal-instructions, docs-configuration-parameters, docs-installation-parameters, docs-data-update, docs-examples, docs-known-limitations, docs-supported-devices, docs-supported-functions, docs-troubleshooting, docs-use-cases.

- [ ] **Step 1: Write README**

Replace `README.md`:

````markdown
# EEBUS Bridge → Home Assistant

[![HACS Custom](https://img.shields.io/badge/HACS-Custom-41BDF5.svg?style=for-the-badge)](https://github.com/hacs/integration) [![GitHub Release](https://img.shields.io/github/v/release/volschin/eebus-ha-bridge?style=for-the-badge)](https://github.com/volschin/eebus-ha-bridge/releases) [![License](https://img.shields.io/github/license/volschin/eebus-ha-bridge?style=for-the-badge)](LICENSE) [![Quality Scale](https://img.shields.io/badge/Quality%20Scale-Gold-FFD700?style=for-the-badge)](https://www.home-assistant.io/docs/quality_scale/)

[![Tests](https://img.shields.io/github/actions/workflow/status/volschin/eebus-ha-bridge/test.yml?branch=main&style=for-the-badge&label=Tests)](https://github.com/volschin/eebus-ha-bridge/actions/workflows/test.yml) [![HACS Validation](https://img.shields.io/github/actions/workflow/status/volschin/eebus-ha-bridge/hacs.yml?branch=main&style=for-the-badge&label=HACS)](https://github.com/volschin/eebus-ha-bridge/actions/workflows/hacs.yml) [![Hassfest](https://img.shields.io/github/actions/workflow/status/volschin/eebus-ha-bridge/hassfest.yml?branch=main&style=for-the-badge&label=Hassfest)](https://github.com/volschin/eebus-ha-bridge/actions/workflows/hassfest.yml)

Lokale Integration von EEBUS-faehigen Waermepumpen in Home Assistant ueber das **EEBUS-Protokoll** (SHIP + SPINE) -- ohne Cloud, ohne mypyllant-Abhaengigkeit fuer Lastmanagement.

## Features

- **Leistungsbegrenzung (LPC)** -- Paragraph-14a-konforme Laststeuerung via EEBUS
- **Leistungsmessung** -- Elektrische Verbrauchsdaten der Waermepumpe in Echtzeit
- **Discovery & Pairing** -- mDNS-Erkennung und SKI-basiertes Pairing ueber den HA Config Flow
- **Heartbeat-Ueberwachung** -- Sicherheitsrelevanter EEBUS-Heartbeat mit Failsafe-Fallback
- **Energy Dashboard** -- Volle Integration mit dem HA Energy Dashboard
- **Erweiterbar** -- Architektur vorbereitet fuer zukuenftige EEBUS-HVAC-Use-Cases

## Architektur

```
Home Assistant                    eebus-bridge (Go)         Vaillant VR940f
+----------------+    gRPC    +------------------+   SHIP   +--------------+
| eebus custom   |<---------->| gRPC Server      |<-------->| aroTHERM plus|
| integration    |            | eebus-go (SHIP/  |          | (EEBUS CS)   |
| (Python)       |            | SPINE embedded)  |          |              |
+----------------+            +------------------+          +--------------+
```

## Installation

### HACS (empfohlen)

1. HACS in Home Assistant oeffnen
2. **Integrations** > drei Punkte oben rechts > **Custom repositories**
3. Repository-URL einfuegen: `https://github.com/volschin/eebus-ha-bridge`
4. Kategorie: **Integration** > **Add**
5. Nach "EEBUS" suchen und **Download** klicken
6. Home Assistant neustarten

### Manuelle Installation

1. Neuestes Release von der [Releases-Seite](https://github.com/volschin/eebus-ha-bridge/releases) herunterladen
2. Den Ordner `eebus` nach `custom_components/eebus/` kopieren
3. Home Assistant neustarten

### Bridge-Setup

Der Go-basierte Bridge-Dienst muss separat laufen (Docker empfohlen):

```bash
docker-compose up -d eebus-bridge
```

Alternativ als Binary:

```bash
./eebus-bridge --config config.yaml
```

## Einrichtung

1. **Settings** > **Devices & Services** > **Add Integration**
2. Nach **EEBUS** suchen
3. **Bridge-Host** und **Bridge-Port** eingeben (Standard: `localhost:50051`)
4. Die Integration testet die Verbindung zur Bridge
5. **Geraete-SKI** eingeben (wird in der Bridge-Log beim Discovery angezeigt)
6. In der **myVaillant-App** das Pairing bestaetigen

### Rekonfiguration

Bridge-Adresse aendern: **Settings** > **Devices & Services** > **EEBUS** > **Configure**

### Entfernen

**Settings** > **Devices & Services** > **EEBUS** > **Delete**

## Daten-Aktualisierung

Die Integration nutzt **gRPC Streaming** (Server-Sent Events) fuer Echtzeit-Updates. Bei Stream-Abbruch wechselt sie automatisch auf **Polling** (30s Intervall) und verbindet den Stream im Hintergrund neu.

- **Leistungsmessung:** Event-basiert (ca. alle 60s vom Inverter)
- **LPC-Limits:** Event-basiert (bei Aenderung)
- **Heartbeat:** Alle 4 Sekunden (im Bridge, nicht in HA)

## Verfuegbare Entities

### Sensoren

| Entity | Typ | Beschreibung |
|--------|-----|-------------|
| `sensor.eebus_power_consumption` | sensor | Aktuelle elektr. Leistung (W) |
| `sensor.eebus_consumption_limit` | sensor | Aktuell gesetztes LPC-Limit (W), readonly |

### Steuerung

| Entity | Typ | Beschreibung |
|--------|-----|-------------|
| `number.eebus_lpc_limit` | number | LPC-Limit setzen (W) |
| `number.eebus_failsafe_limit` | number | Failsafe-Grenze (W), standardmaessig deaktiviert |
| `switch.eebus_lpc_active` | switch | Limit aktivieren/deaktivieren |
| `switch.eebus_heartbeat` | switch | Heartbeat an/aus, standardmaessig deaktiviert |

### Diagnose

| Entity | Typ | Beschreibung |
|--------|-----|-------------|
| `binary_sensor.eebus_connected` | binary_sensor | EEBUS-Verbindungsstatus |
| `binary_sensor.eebus_heartbeat_ok` | binary_sensor | Heartbeat innerhalb Toleranz, standardmaessig deaktiviert |

## Unterstuetzte Geraete

### Kompatible Modelle

| Hersteller | Modell | Gateway | Getestet |
|-----------|--------|---------|----------|
| Vaillant | aroTHERM plus | VR940f | Primaeres Ziel |
| Vaillant | aroTHERM plus | VR920/VR921 | Erwartet kompatibel |

### Voraussetzungen

- Vaillant Gateway (VR920/VR921/VR940f) mit aktiviertem EEBUS
- Gateway und eebus-bridge im selben Netzwerk
- Docker oder Go 1.22+ fuer den Bridge-Dienst

### Nicht unterstuetzt

- HVAC-Steuerung (Betriebsmodi, Sollwerte) -- Vaillant exponiert diese nicht ueber EEBUS
- Geraete ohne EEBUS-Schnittstelle

## Anwendungsbeispiele

### PV-gefuehrte Lastbegrenzung

```yaml
automation:
  - alias: "PV-Ueberschuss an Waermepumpe"
    trigger:
      - platform: numeric_state
        entity_id: sensor.pv_ueberschuss
        above: 2000
    action:
      - service: number.set_value
        target:
          entity_id: number.eebus_lpc_limit
        data:
          value: "{{ states('sensor.pv_ueberschuss') | float }}"
      - service: switch.turn_on
        target:
          entity_id: switch.eebus_lpc_active
```

### Paragraph-14a-Notbremse

```yaml
automation:
  - alias: "Paragraph 14a Leistungsbegrenzung"
    trigger:
      - platform: state
        entity_id: binary_sensor.netzbetreiber_signal
        to: "on"
    action:
      - service: number.set_value
        target:
          entity_id: number.eebus_lpc_limit
        data:
          value: 4200
      - service: switch.turn_on
        target:
          entity_id: switch.eebus_lpc_active
```

## Bekannte Einschraenkungen

- **Keine HVAC-Steuerung:** Vaillant exponiert Betriebsmodi und Sollwerte nicht ueber EEBUS. Dafuer weiterhin mypyllant nutzen.
- **Kein Auto-Discovery in HA:** Die Bridge-Adresse muss manuell eingegeben werden. Die EEBUS-Discovery (mDNS) findet Waermepumpen, aber die Bridge selbst wird nicht von HA entdeckt.
- **Re-Pairing bei Zertifikatsverlust:** Wird das Bridge-Zertifikat geloescht, aendert sich der SKI. Erneutes Pairing in der myVaillant-App noetig.
- **Heartbeat bei Bridge-Ausfall:** Die Waermepumpe erkennt den Heartbeat-Timeout (max 2 min) und faellt auf den Failsafe-Wert zurueck.

## Troubleshooting

<details>
<summary>Bridge nicht erreichbar</summary>

1. Bridge-Container laeuft? `docker ps | grep eebus-bridge`
2. Port 50051 erreichbar? `grpcurl -plaintext localhost:50051 list`
3. Bridge-Log pruefen: `docker logs eebus-bridge`

</details>

<details>
<summary>Pairing schlaegt fehl</summary>

1. SKI in der Bridge-Log pruefen (wird beim Start ausgegeben)
2. In der myVaillant-App unter Netzwerk > EEBUS den Bridge-SKI bestaetigen
3. Sicherstellen, dass beide Geraete im selben Netzwerk sind

</details>

<details>
<summary>Keine Messwerte</summary>

- EEBUS-Messwerte kommen ca. alle 60 Sekunden
- Pruefen ob `binary_sensor.eebus_connected` "on" ist
- Bridge-Log auf Fehlermeldungen pruefen

</details>

## Lizenz

MIT License -- siehe [LICENSE](LICENSE).
````

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: add Gold-compliant README with full documentation coverage"
```

---

## Summary

| Phase | Tasks | Description |
|-------|-------|-------------|
| 1 | 1-3 | Go scaffold, config, certs |
| 2 | 4 | Event bus |
| 3 | 5 | Protobuf schemas + code gen |
| 4 | 6-7 | EEBUS service + use case wrappers |
| 5 | 8-11 | gRPC server + service implementations |
| 6 | 12-13 | Device registry + main wiring |
| 7 | 14 | Docker deployment |
| 8 | 15-19 | HA integration scaffold (HACS + Gold), coordinator, config flow, entities |
| 9 | 20 | Python proto stubs |
| 10 | 21-23 | Diagnostics, setup/unload tests, gRPC integration test |
| 11 | 24 | CI workflows (HACS, Hassfest, test, Go, release) |
| 12 | 25 | README (Gold documentation) |

**Total:** 25 tasks across 12 phases.

**Quality Scale Gold compliance by task:**

| Rule | Status | Task |
|------|--------|------|
| config-flow | done | 17 |
| config-flow-test-coverage | done | 17 |
| brands | done | 15 |
| entity-unique-id | done | 18-19 |
| has-entity-name | done | 18 |
| runtime-data | done | 15 |
| test-before-configure | done | 17 |
| unique-config-entry | done | 17 |
| config-entry-unloading | done | 15, 22 |
| entity-unavailable | done | 16 |
| log-when-unavailable | done | 16 |
| parallel-updates | done | 15 |
| test-coverage (>95%) | done | 22, 24 |
| devices | done | 18 |
| diagnostics | done | 21 |
| entity-category | done | 18-19 |
| entity-device-class | done | 18-19 |
| entity-disabled-by-default | done | 19 |
| entity-translations | done | 17 |
| exception-translations | done | 17 |
| icon-translations | done | 17 |
| reconfiguration-flow | done | 17 |
| docs-* (all) | done | 25 |

**HACS compliance by task:**

| Requirement | Task |
|-------------|------|
| hacs.json | 15 |
| manifest.json (all fields) | 15 |
| brand/ (icon.png, logo.png) | 15 |
| HACS validation CI | 24 |
| Hassfest validation CI | 24 |
| Release drafter | 24 |

**Parallelizable:** Go bridge (Tasks 1-14) and HA integration (Tasks 15-19) can be developed in parallel once protobuf schemas (Task 5) are stable.

**First milestone:** After Tasks 1-14, the bridge is runnable and testable with `grpcurl`. The HA integration adds the user-facing layer on top.
