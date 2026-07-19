package eebus

import (
	"bytes"
	"log"
	"net"
	"strings"
	"testing"

	shipcert "github.com/enbility/ship-go/cert"
	"github.com/volschin/eebus-bridge/internal/config"
)

func TestBridgeServiceLifecycleAndEntities(t *testing.T) {
	certificate, err := shipcert.CreateCertificate("test", "test", "DE", "bridge-service")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		EEBUS: config.EEBUSConfig{Port: port, Vendor: "Test", Brand: "Test", Model: "Test", Serial: "bridge-service"},
		Experimental: config.ExperimentalConfig{
			MGCPProvider: true,
			VAPDProvider: true,
			VABDProvider: true,
		},
		Logging: config.LoggingConfig{ShipLog: true, ShipTrace: true},
	}
	bus := NewEventBus()
	bridge, err := NewBridgeService(cfg, certificate, bus)
	if err != nil {
		t.Fatalf("NewBridgeService() error = %v", err)
	}
	if bridge.Service() == nil || bridge.Callbacks() == nil {
		t.Fatalf("bridge dependencies are incomplete: %+v", bridge)
	}
	if err := bridge.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	t.Cleanup(bridge.Shutdown)
	if bridge.LocalEntity() == nil || bridge.GridEntity() == nil || bridge.PVEntity() == nil || bridge.BatteryEntity() == nil {
		t.Fatalf("bridge entities are incomplete: %+v", bridge)
	}
	bridge.RegisterRemoteSKI("ab:cd")
	bridge.UnregisterRemoteSKI("ab:cd")
	if err := bridge.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
}

func TestBridgeServiceWithoutProviderEntities(t *testing.T) {
	certificate, err := shipcert.CreateCertificate("test", "test", "DE", "bridge-service-minimal")
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := NewBridgeService(&config.Config{EEBUS: config.EEBUSConfig{
		Port: 49879, Vendor: "Test", Brand: "Test", Model: "Test", Serial: "bridge-service-minimal",
	}}, certificate, NewEventBus())
	if err != nil {
		t.Fatal(err)
	}
	if err := bridge.Setup(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Shutdown)
	if bridge.GridEntity() != nil || bridge.PVEntity() != nil || bridge.BatteryEntity() != nil {
		t.Fatal("provider entities were created while providers were disabled")
	}
}

func TestShipLoggerForwardsEveryLevelAndTraceGate(t *testing.T) {
	var output bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&output)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})

	silentTrace := &shipLogger{}
	silentTrace.Trace("hidden")
	silentTrace.Tracef("hidden %d", 1)
	if output.Len() != 0 {
		t.Fatalf("disabled trace output = %q", output.String())
	}

	logger := &shipLogger{trace: true}
	logger.Trace("trace")
	logger.Tracef("trace %d", 2)
	logger.Debug("debug")
	logger.Debugf("debug %d", 3)
	logger.Info("info")
	logger.Infof("info %d", 4)
	logger.Error("error")
	logger.Errorf("error %d", 5)
	for _, marker := range []string{"[SHIP TRACE]", "[SHIP DEBUG]", "[SHIP INFO]", "[SHIP ERROR]"} {
		if !strings.Contains(output.String(), marker) {
			t.Fatalf("log output %q does not contain %q", output.String(), marker)
		}
	}
}

func TestTrustControllerRegistrationAndDependencyGuards(t *testing.T) {
	registry := NewDeviceRegistry()
	bus := NewEventBus()
	bridge := &recordingRemoteTrustService{registry: registry, events: bus.Subscribe()}
	defer bus.Unsubscribe(bridge.events)
	controller := &TrustController{bridge: bridge, registry: registry, bus: bus}
	if err := controller.RegisterSKI("ab:cd"); err != nil {
		t.Fatalf("RegisterSKI() error = %v", err)
	}
	if len(bridge.registerCalls) != 1 || bridge.registerCalls[0] != "ab:cd" {
		t.Fatalf("register calls = %v", bridge.registerCalls)
	}
	if snapshot, ok := registry.DeviceHealth("ab:cd"); !ok || !snapshot.Trusted {
		t.Fatalf("registered device health = (%+v, %t)", snapshot, ok)
	}

	if err := (*TrustController)(nil).RegisterSKI("ab:cd"); err == nil {
		t.Fatal("nil controller RegisterSKI() succeeded")
	}
	if err := NewTrustController(nil, registry, bus).RegisterSKI("ab:cd"); err == nil {
		t.Fatal("controller without bridge RegisterSKI() succeeded")
	}
	if got := NewTrustController(&BridgeService{}, registry, bus); got.bridge == nil {
		t.Fatal("NewTrustController() discarded bridge")
	}

	checks := []*TrustController{
		nil,
		{bridge: bridge},
		{bridge: bridge, registry: registry},
	}
	for index, check := range checks {
		if err := check.UnregisterSKI("ab:cd"); err == nil {
			t.Fatalf("UnregisterSKI dependency check %d succeeded", index)
		}
	}
}
