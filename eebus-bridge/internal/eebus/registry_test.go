package eebus_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

type fakeClock struct {
	now time.Time
}

func capability(t *testing.T, registry *eebus.DeviceRegistry, ski string, id eebus.Capability) eebus.DeviceCapability {
	t.Helper()
	entries, _ := registry.DeviceCapabilities(ski)
	for _, entry := range entries {
		if entry.ID == id {
			return entry
		}
	}
	t.Fatalf("capability %d not found", id)
	return eebus.DeviceCapability{}
}

func TestCapabilityContractLocalDisabled(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)}
	registry := eebus.NewDeviceRegistryWithClock(clock)
	registry.SetLocalCapabilityEnabled(eebus.CapabilityOHPCF, false)
	registry.AddDevice("ab:cd", eebus.DeviceInfo{})

	entry := capability(t, registry, "ab:cd", eebus.CapabilityOHPCF)
	if entry.State != eebus.CapabilityStateUnsupported || entry.Reason != eebus.CapabilityReasonLocalDisabled {
		t.Fatalf("disabled capability = %+v", entry)
	}
}

func TestCapabilityContractRemoteNotAdvertisedIsSticky(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	registry.RecordCapabilitySupport("ab:cd", eebus.CapabilityDHW, false)
	registry.RecordCapabilityRead("ab:cd", eebus.CapabilityDHW, nil)

	entry := capability(t, registry, "ABCD", eebus.CapabilityDHW)
	if entry.State != eebus.CapabilityStateUnsupported || entry.Reason != eebus.CapabilityReasonRemoteNotAdvertised {
		t.Fatalf("remote unsupported capability = %+v", entry)
	}
}

func TestAggregateCapabilitySupportUsesAnyAdvertisedSource(t *testing.T) {
	tests := []struct {
		name        string
		temperature bool
		systemFn    bool
		wantState   eebus.CapabilityState
	}{
		{"temperature only", true, false, eebus.CapabilityStateUnknown},
		{"system function only", false, true, eebus.CapabilityStateUnknown},
		{"neither", false, false, eebus.CapabilityStateUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := eebus.NewDeviceRegistry()
			registry.RecordCapabilitySourceSupport(
				"ab:cd", eebus.CapabilityRoomHeating, "room_heating_temperature", tt.temperature,
			)
			registry.RecordCapabilitySourceSupport(
				"ab:cd", eebus.CapabilityRoomHeating, "room_heating_system_function", tt.systemFn,
			)

			entry := capability(t, registry, "ABCD", eebus.CapabilityRoomHeating)
			if entry.State != tt.wantState {
				t.Fatalf("aggregate capability = %+v, want state %v", entry, tt.wantState)
			}
			if tt.wantState == eebus.CapabilityStateUnsupported &&
				entry.Reason != eebus.CapabilityReasonRemoteNotAdvertised {
				t.Fatalf("unsupported aggregate capability = %+v", entry)
			}
		})
	}
}

func TestCapabilityContractEntityNotBound(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	registry.RecordCapabilityEntityNotBound("ab:cd", eebus.CapabilityRoomHeating)

	entry := capability(t, registry, "ABCD", eebus.CapabilityRoomHeating)
	if entry.State != eebus.CapabilityStateTemporarilyUnavailable || entry.Reason != eebus.CapabilityReasonEntityNotBound {
		t.Fatalf("unbound capability = %+v", entry)
	}
}

func TestCapabilityMissingEntityOnKnownDeviceIsRemoteUnsupported(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice("ab:cd", eebus.DeviceInfo{RemoteEntities: []spineapi.EntityRemoteInterface{nil}})
	registry.RecordCapabilityMissingEntity("ab:cd", eebus.CapabilityOHPCF)

	entry := capability(t, registry, "ABCD", eebus.CapabilityOHPCF)
	if entry.State != eebus.CapabilityStateUnsupported || entry.Reason != eebus.CapabilityReasonRemoteNotAdvertised {
		t.Fatalf("missing advertised capability = %+v", entry)
	}
}

func TestCapabilityContractTemporaryReadFailure(t *testing.T) {
	changedAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: changedAt}
	registry := eebus.NewDeviceRegistryWithClock(clock)
	registry.RecordCapabilityRead("ab:cd", eebus.CapabilityMonitoring, errors.New("transport detail"))

	entry := capability(t, registry, "ABCD", eebus.CapabilityMonitoring)
	if entry.State != eebus.CapabilityStateTemporarilyUnavailable || entry.Reason != eebus.CapabilityReasonReadFailed || entry.LastChanged != changedAt {
		t.Fatalf("failed capability = %+v", entry)
	}
}

func TestCapabilityContractDisconnectUsesSameRegistry(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	registry.MarkConnected("ab:cd")
	registry.RecordCapabilityRead("ab:cd", eebus.CapabilityLPC, nil)
	registry.MarkDisconnected("ab:cd")

	entry := capability(t, registry, "ABCD", eebus.CapabilityLPC)
	if entry.State != eebus.CapabilityStateTemporarilyUnavailable || entry.Reason != eebus.CapabilityReasonDeviceDisconnected {
		t.Fatalf("disconnected capability = %+v", entry)
	}
}

func TestCapabilitySupportRemovalAfterDisconnectPreservesDisconnectReason(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	registry.MarkConnected("ab:cd")
	registry.RecordCapabilityRead("ab:cd", eebus.CapabilityLPC, nil)
	registry.MarkDisconnected("ab:cd")
	registry.RecordCapabilitySupport("ab:cd", eebus.CapabilityLPC, false)

	entry := capability(t, registry, "ABCD", eebus.CapabilityLPC)
	if entry.State != eebus.CapabilityStateTemporarilyUnavailable || entry.Reason != eebus.CapabilityReasonDeviceDisconnected {
		t.Fatalf("post-disconnect support removal capability = %+v", entry)
	}
}

func TestLateCapabilityUpdatesAfterDisconnectPreserveDisconnectReason(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	registry.MarkConnected("ab:cd")
	registry.RecordCapabilityRead("ab:cd", eebus.CapabilityLPC, nil)
	registry.MarkDisconnected("ab:cd")

	registry.RecordCapabilityRead("ab:cd", eebus.CapabilityLPC, nil)
	registry.RecordCapabilityMissingEntity("ab:cd", eebus.CapabilityLPC)
	registry.RecordCapabilityEntityNotBound("ab:cd", eebus.CapabilityLPC)

	entry := capability(t, registry, "ABCD", eebus.CapabilityLPC)
	if entry.State != eebus.CapabilityStateTemporarilyUnavailable || entry.Reason != eebus.CapabilityReasonDeviceDisconnected {
		t.Fatalf("late post-disconnect capability = %+v", entry)
	}
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.now = c.now.Add(duration)
}

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

func TestRegistryExplicitTrustStartsNewLifetimeAfterRemoval(t *testing.T) {
	reg := eebus.NewDeviceRegistry()
	reg.AddDevice("ski-123", eebus.DeviceInfo{})
	reg.RemoveDevice("ski-123")
	reg.AddDevice("ski-123", eebus.DeviceInfo{Brand: "late callback"})
	if reg.KnownDevice("ski-123") {
		t.Fatal("late add started a new device lifetime")
	}

	reg.MarkTrusted("ski-123")
	if !reg.KnownDevice("ski-123") {
		t.Fatal("explicit trust did not start a new device lifetime")
	}
}

func TestRegistryClearEntities(t *testing.T) {
	reg := eebus.NewDeviceRegistry()
	reg.AddDevice("ski-c", eebus.DeviceInfo{
		Brand:          "Vaillant",
		Model:          "VR940f",
		UseCases:       []string{"ohpcf"},
		RemoteEntities: []spineapi.EntityRemoteInterface{nil},
	})

	reg.ClearEntities("ski-c")

	info, ok := reg.GetDevice("ski-c")
	if !ok {
		t.Fatal("device gone after ClearEntities; classification metadata must survive")
	}
	if len(info.RemoteEntities) != 0 {
		t.Errorf("RemoteEntities = %d, want 0 (cleared)", len(info.RemoteEntities))
	}
	if info.RemoteDevice != nil {
		t.Error("RemoteDevice not cleared")
	}
	if info.Brand != "Vaillant" || info.Model != "VR940f" {
		t.Error("classification metadata must be preserved across disconnect")
	}
	if len(info.UseCases) != 1 {
		t.Errorf("UseCases = %d, want 1 (preserved)", len(info.UseCases))
	}
}

func TestRegistryClearEntitiesUnknownSKI(t *testing.T) {
	reg := eebus.NewDeviceRegistry()
	reg.ClearEntities("never-seen") // must not panic nor create an entry
	if _, ok := reg.GetDevice("never-seen"); ok {
		t.Error("ClearEntities must not create an entry for an unknown SKI")
	}
}

func TestRegistryUpsertDeviceClassification(t *testing.T) {
	reg := eebus.NewDeviceRegistry()

	// First observation establishes the device with real Bosch metadata.
	changed := reg.UpsertDeviceClassification(
		"ski-bosch", "Bosch", "Compress 5800i", "SN-1", "HeatPumpAppliance", "4.2.1", "R3",
	)
	if !changed {
		t.Fatal("first classification update was not reported as changed")
	}

	info, ok := reg.GetDevice("ski-bosch")
	if !ok {
		t.Fatal("device not found")
	}
	if info.Brand != "Bosch" || info.Model != "Compress 5800i" {
		t.Errorf("got Brand=%q Model=%q, want Bosch/Compress 5800i", info.Brand, info.Model)
	}
	if info.Serial != "SN-1" || info.DeviceType != "HeatPumpAppliance" {
		t.Errorf("got Serial=%q DeviceType=%q", info.Serial, info.DeviceType)
	}
	if info.SoftwareRevision != "4.2.1" || info.HardwareRevision != "R3" {
		t.Errorf("got SoftwareRevision=%q HardwareRevision=%q", info.SoftwareRevision, info.HardwareRevision)
	}

	// Empty fields in a later partial update must not clear discovered values.
	if reg.UpsertDeviceClassification("ski-bosch", "", "", "", "", "", "") {
		t.Fatal("empty classification update was reported as changed")
	}
	info, _ = reg.GetDevice("ski-bosch")
	if info.Brand != "Bosch" || info.Model != "Compress 5800i" {
		t.Errorf("partial update cleared fields: Brand=%q Model=%q", info.Brand, info.Model)
	}
}

func TestRegistryClassificationCannotRecreateRemovedDevice(t *testing.T) {
	reg := eebus.NewDeviceRegistry()
	reg.AddDevice("removed", eebus.DeviceInfo{})
	reg.RemoveDevice("removed")
	if reg.UpsertDeviceClassification("removed", "vendor", "model", "serial", "type", "sw", "hw") {
		t.Fatal("classification recreated a removed device")
	}
}

func TestNormalizeSKI(t *testing.T) {
	cases := map[string]string{
		"abcdef":            "ABCDEF",
		"  ab cd ef  ":      "ABCDEF",
		"ab:cd-ef":          "ABCDEF",
		"ab\tcd\nef":        "ABCDEF",
		"AbCdEf":            "ABCDEF",
		"\t ab:CD-ef \r\n":  "ABCDEF",
		" aB:cD-ef\t12:34 ": "ABCDEF1234",
		"":                  "",
	}
	for in, want := range cases {
		if got := eebus.NormalizeSKI(in); got != want {
			t.Errorf("NormalizeSKI(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShortSKI(t *testing.T) {
	if got, want := eebus.ShortSKI("ab:cd:ef:12:34"), "…EF1234"; got != want {
		t.Errorf("ShortSKI() = %q, want %q", got, want)
	}
}

func TestRegistrySKINormalizationConsistency(t *testing.T) {
	reg := eebus.NewDeviceRegistry()

	// Stored with lowercase + spaces; looked up with uppercase, no spaces.
	reg.AddDevice("ab cd 12", eebus.DeviceInfo{Brand: "Bosch"})

	if _, ok := reg.GetDevice("ABCD12"); !ok {
		t.Error("normalized lookup failed: device stored under non-normalized key")
	}

	// Classification reported under a differently-cased SKI must land on the
	// same record so brand/model are not split across two keys.
	reg.UpsertDeviceClassification("ABCD12", "Bosch", "Compress 5800i", "", "", "", "")
	info, ok := reg.GetDevice("ab cd 12")
	if !ok || info.Model != "Compress 5800i" {
		t.Errorf("classification not merged onto same device: ok=%v model=%q", ok, info.Model)
	}

	reg.RemoveDevice("Ab Cd 12")
	if _, ok := reg.GetDevice("ABCD12"); ok {
		t.Error("normalized removal failed")
	}
}

func TestRegistryListDevices(t *testing.T) {
	reg := eebus.NewDeviceRegistry()
	reg.AddDevice("cc:03", eebus.DeviceInfo{Brand: "C"})
	reg.AddDevice("AA-01", eebus.DeviceInfo{Brand: "A"})
	reg.AddDevice(" bb 02 ", eebus.DeviceInfo{Brand: "B"})

	devices := reg.ListDevices()
	if len(devices) != 3 {
		t.Fatalf("len(devices) = %d, want 3", len(devices))
	}
	for index, want := range []string{"AA01", "BB02", "CC03"} {
		if devices[index].SKI != want {
			t.Errorf("devices[%d].SKI = %q, want %q", index, devices[index].SKI, want)
		}
	}
}

func TestRegistryReadProjectionsAreDeepCopies(t *testing.T) {
	reg := eebus.NewDeviceRegistry()
	reg.AddDevice("AA11", eebus.DeviceInfo{
		UseCases:       []string{"monitoring"},
		RemoteEntities: []spineapi.EntityRemoteInterface{nil},
		Entities: []eebus.EntityInfo{{
			Address: "1", Features: []string{"Measurement/client"},
		}},
	})

	device, ok := reg.GetDevice("AA11")
	if !ok {
		t.Fatal("device missing")
	}
	device.UseCases[0] = "mutated"
	device.RemoteEntities[0] = mocks.NewEntityRemoteInterface(t)
	device.Entities[0].Features[0] = "mutated"
	listed := reg.ListDevices()
	listed[0].UseCases[0] = "also-mutated"

	again, _ := reg.GetDevice("AA11")
	if again.UseCases[0] != "monitoring" || again.RemoteEntities[0] != nil || again.Entities[0].Features[0] != "Measurement/client" {
		t.Fatalf("mutated read projection changed registry: %+v", again)
	}
}

func TestRegistryParallelCatalogHealthAndCapabilityProjections(t *testing.T) {
	reg := eebus.NewDeviceRegistry()
	var wait sync.WaitGroup
	for worker := range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := range 100 {
				ski := fmt.Sprintf("%02d%02d", worker, iteration%4)
				reg.AddDevice(ski, eebus.DeviceInfo{UseCases: []string{"monitoring"}})
				reg.UpsertDeviceClassification(ski, "vendor", "model", "serial", "type", "software", "hardware")
				reg.MarkConnected(ski)
				reg.RecordCapabilityRead(ski, eebus.CapabilityMonitoring, nil)
				reg.GetDevice(ski)
				reg.ListDevices()
				reg.ListDeviceHealth()
				reg.DeviceCapabilities(ski)
				reg.MarkDisconnected(ski)
			}
		}()
	}
	wait.Wait()
}

func TestRegistryConcurrentLifecycleOperationsDoNotResurrectOrMisclassify(t *testing.T) {
	reg := eebus.NewDeviceRegistry()
	for iteration := range 100 {
		ski := fmt.Sprintf("device-%d", iteration)
		reg.AddDevice(ski, eebus.DeviceInfo{})
		reg.MarkConnected(ski)

		var wait sync.WaitGroup
		wait.Add(3)
		go func() {
			defer wait.Done()
			reg.UpsertObservation(ski, nil, nil, "monitoring")
		}()
		go func() {
			defer wait.Done()
			reg.RecordCapabilityRead(ski, eebus.CapabilityMonitoring, nil)
		}()
		go func() {
			defer wait.Done()
			reg.RemoveDevice(ski)
		}()
		wait.Wait()

		if reg.KnownDevice(ski) {
			t.Fatalf("iteration %d: concurrent callback resurrected removed SKI", iteration)
		}
		if _, ok := reg.DeviceCapabilities(ski); ok {
			t.Fatalf("iteration %d: concurrent callback recreated capabilities", iteration)
		}
	}

	const disconnectedSKI = "capability-race"
	reg.MarkConnected(disconnectedSKI)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		reg.MarkUntrusted(disconnectedSKI)
	}()
	go func() {
		defer wait.Done()
		reg.RecordCapabilityRead(disconnectedSKI, eebus.CapabilityMonitoring, nil)
	}()
	wait.Wait()
	entry := capability(t, reg, disconnectedSKI, eebus.CapabilityMonitoring)
	if entry.Reason != eebus.CapabilityReasonDeviceDisconnected {
		t.Fatalf("capability race result = %+v, want disconnected", entry)
	}
}

func TestRegistryFirstAvailableEntityRequiresOneDevice(t *testing.T) {
	reg := eebus.NewDeviceRegistry()
	entityA := mocks.NewEntityRemoteInterface(t)
	entityB := mocks.NewEntityRemoteInterface(t)

	reg.AddDevice("ab:cd-ef", eebus.DeviceInfo{RemoteEntities: []spineapi.EntityRemoteInterface{entityA}})
	reg.AddDevice(" AB-CD-EF ", eebus.DeviceInfo{RemoteEntities: []spineapi.EntityRemoteInterface{entityA}})
	resolution := reg.FirstAvailableEntity()
	if resolution.Ambiguous() || resolution.DeviceCount != 1 || resolution.Entity != entityA {
		t.Fatalf("format variants resolved as %+v, want one physical device", resolution)
	}

	reg.AddDevice("DDEEFF", eebus.DeviceInfo{RemoteEntities: []spineapi.EntityRemoteInterface{entityB}})
	resolution = reg.FirstAvailableEntity()
	if !resolution.Ambiguous() || resolution.DeviceCount != 2 {
		t.Fatalf("two devices resolved as %+v, want ambiguity", resolution)
	}
	if reg.FirstEntity("ab cd ef") != entityA || reg.FirstEntity("dd:ee:ff") != entityB {
		t.Error("explicit registry lookup did not preserve device identity")
	}
	if reg.FirstEntity("unknown") != nil {
		t.Error("unknown explicit registry lookup returned an entity")
	}
}

func TestRegistryEntityHelpersEmpty(t *testing.T) {
	reg := eebus.NewDeviceRegistry()

	if entities := reg.Entities("unknown"); len(entities) != 0 {
		t.Errorf("Entities unknown = %d, want 0", len(entities))
	}
	if entity := reg.FirstEntityForType("unknown", "HeatPumpAppliance"); entity != nil {
		t.Error("FirstEntityForType unknown returned entity")
	}
}

func TestDeviceConnectionTracksRealTransitionsOnly(t *testing.T) {
	connectedAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: connectedAt}
	reg := eebus.NewDeviceRegistryWithClock(clock)

	if connected, transition, known := reg.DeviceConnection("unknown"); connected || !transition.IsZero() || known {
		t.Fatalf("DeviceConnection(unknown) = (%t, %s, %t), want (false, zero, false)", connected, transition, known)
	}
	reg.MarkDisconnected("unknown")
	if _, _, known := reg.DeviceConnection("unknown"); known {
		t.Fatal("MarkDisconnected created monitoring state for an unknown SKI")
	}

	reg.MarkConnected("ab:cd-ef")
	connected, transition, known := reg.DeviceConnection("AB-CD-EF")
	if !connected || transition != connectedAt || !known {
		t.Fatalf("DeviceConnection(connected) = (%t, %s, %t), want (true, %s, true)", connected, transition, known, connectedAt)
	}

	clock.Advance(time.Minute)
	reg.MarkConnected("a b c d e f")
	_, transition, _ = reg.DeviceConnection("ABCDEF")
	if transition != connectedAt {
		t.Errorf("redundant MarkConnected changed transition to %s, want %s", transition, connectedAt)
	}

	disconnectedAt := clock.now.Add(time.Minute)
	clock.Advance(time.Minute)
	reg.MarkDisconnected("ABCDEF")
	connected, transition, known = reg.DeviceConnection("ab:cd:ef")
	if connected || transition != disconnectedAt || !known {
		t.Fatalf("DeviceConnection(disconnected) = (%t, %s, %t), want (false, %s, true)", connected, transition, known, disconnectedAt)
	}

	clock.Advance(time.Minute)
	reg.MarkDisconnected("ABCDEF")
	_, transition, _ = reg.DeviceConnection("ABCDEF")
	if transition != disconnectedAt {
		t.Errorf("redundant MarkDisconnected changed transition to %s, want %s", transition, disconnectedAt)
	}
}

func TestDeviceConnectionUnknownAfterDeviceRemoval(t *testing.T) {
	reg := eebus.NewDeviceRegistry()
	reg.MarkConnected("ski-1")
	reg.RemoveDevice("ski-1")

	connected, transition, known := reg.DeviceConnection("ski-1")
	if connected || !transition.IsZero() || known {
		t.Errorf("DeviceConnection(removed) = (%t, %s, %t), want (false, zero, false)", connected, transition, known)
	}
}

func TestStaleDevicesNoConnectedDevice(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)}
	reg := eebus.NewDeviceRegistryWithClock(clock)

	clock.Advance(24 * time.Hour)
	if got := reg.StaleDevices(time.Minute, time.Minute); len(got) != 0 {
		t.Errorf("StaleDevices() = %v, want none with no connected device", got)
	}
}

func TestStaleDevicesWithinThreshold(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)}
	reg := eebus.NewDeviceRegistryWithClock(clock)
	reg.MarkConnected("ski-1")
	reg.RecordMonitoringSuccess("ski-1")

	clock.Advance(3 * time.Minute)
	if got := reg.StaleDevices(10*time.Minute, 2*time.Minute); len(got) != 0 {
		t.Errorf("StaleDevices() = %v, want none within the success threshold", got)
	}
}

func TestStaleDevicesExceedsThreshold(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)}
	reg := eebus.NewDeviceRegistryWithClock(clock)
	reg.MarkConnected("ski-1")
	reg.RecordMonitoringSuccess("ski-1")

	clock.Advance(11 * time.Minute)
	if got, want := reg.StaleDevices(10*time.Minute, 2*time.Minute), []string{"SKI1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("StaleDevices() = %v, want %v once the success threshold is exceeded", got, want)
	}
}

func TestStaleDevicesSuccessForOneDeviceDoesNotMaskAnother(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)}
	reg := eebus.NewDeviceRegistryWithClock(clock)
	reg.MarkConnected("aa:aa:aa")
	reg.MarkConnected("bb:bb:bb")
	reg.RecordMonitoringSuccess("aa-aa-aa")

	clock.Advance(3 * time.Minute)
	if got, want := reg.StaleDevices(10*time.Minute, 2*time.Minute), []string{"BBBBBB"}; !reflect.DeepEqual(got, want) {
		t.Errorf("StaleDevices() = %v, want only device B (%v)", got, want)
	}
}

func TestStaleDevicesIgnoresDisconnectedDevice(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)}
	reg := eebus.NewDeviceRegistryWithClock(clock)
	reg.MarkConnected("ski-1")

	clock.Advance(11 * time.Minute)
	reg.MarkDisconnected("ski-1")
	if got := reg.StaleDevices(10*time.Minute, 2*time.Minute); len(got) != 0 {
		t.Errorf("StaleDevices() = %v, want none after a clean disconnect", got)
	}
}

func TestStaleDevicesReconnectStartsFreshGracePeriod(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)}
	reg := eebus.NewDeviceRegistryWithClock(clock)
	reg.MarkConnected("ski-1")

	clock.Advance(3 * time.Minute)
	if got, want := reg.StaleDevices(10*time.Minute, 2*time.Minute), []string{"SKI1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("StaleDevices() before reconnect = %v, want %v", got, want)
	}

	reg.MarkDisconnected("ski-1")
	reg.MarkConnected("ski-1")
	if got := reg.StaleDevices(10*time.Minute, 2*time.Minute); len(got) != 0 {
		t.Fatalf("StaleDevices() immediately after reconnect = %v, want fresh grace period", got)
	}

	clock.Advance(3 * time.Minute)
	if got, want := reg.StaleDevices(10*time.Minute, 2*time.Minute), []string{"SKI1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("StaleDevices() after reconnect grace = %v, want stale again (%v)", got, want)
	}
}

func TestStaleDevicesReconnectRetainsLastSuccessOnlyForDiagnostics(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)}
	reg := eebus.NewDeviceRegistryWithClock(clock)
	reg.MarkConnected("ski-1")
	reg.RecordMonitoringSuccess("ski-1")

	clock.Advance(time.Minute)
	reg.MarkDisconnected("ski-1")
	reg.MarkConnected("ski-1")
	clock.Advance(3 * time.Minute)

	// The old success is younger than the steady-state threshold but must not
	// satisfy the new connection once its fresh grace period has elapsed.
	if got, want := reg.StaleDevices(10*time.Minute, 2*time.Minute), []string{"SKI1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("StaleDevices() = %v, want %v without a success after reconnect", got, want)
	}
	if age, ok := reg.MonitoringLastSuccessAge("s:k-i 1"); !ok || age != 4*time.Minute {
		t.Errorf("MonitoringLastSuccessAge() = (%s, %t), want (4m, true)", age, ok)
	}
}
