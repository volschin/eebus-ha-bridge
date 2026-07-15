package eebus_test

import (
	"testing"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
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
	reg.UpsertDeviceClassification("ski-bosch", "Bosch", "Compress 5800i", "SN-1", "HeatPumpAppliance")

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

	// Empty fields in a later partial update must not clear discovered values.
	reg.UpsertDeviceClassification("ski-bosch", "", "", "", "")
	info, _ = reg.GetDevice("ski-bosch")
	if info.Brand != "Bosch" || info.Model != "Compress 5800i" {
		t.Errorf("partial update cleared fields: Brand=%q Model=%q", info.Brand, info.Model)
	}
}

func TestNormalizeSKI(t *testing.T) {
	cases := map[string]string{
		"abcdef":       "ABCDEF",
		"  ab cd ef  ": "ABCDEF",
		"ab:cd-ef":     "ABCDEF",
		"ab\tcd\nef":   "ABCDEF",
		"AbCdEf":       "ABCDEF",
		"":             "",
	}
	for in, want := range cases {
		if got := eebus.NormalizeSKI(in); got != want {
			t.Errorf("NormalizeSKI(%q) = %q, want %q", in, got, want)
		}
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
	reg.UpsertDeviceClassification("ABCD12", "Bosch", "Compress 5800i", "", "")
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

func TestMonitoringStaleNoTrustedDevice(t *testing.T) {
	reg := eebus.NewDeviceRegistry()
	if reg.MonitoringStale(0) {
		t.Error("MonitoringStale should be false with no trusted device, regardless of threshold")
	}
}

func TestMonitoringStaleWithinThreshold(t *testing.T) {
	reg := eebus.NewDeviceRegistry()
	reg.AddDevice("ski-1", eebus.DeviceInfo{Brand: "Vaillant"})
	reg.RecordMonitoringSuccess()

	if reg.MonitoringStale(time.Minute) {
		t.Error("MonitoringStale should be false right after a recorded success")
	}
}

func TestMonitoringStaleExceedsThreshold(t *testing.T) {
	reg := eebus.NewDeviceRegistry()
	reg.AddDevice("ski-1", eebus.DeviceInfo{Brand: "Vaillant"})
	reg.RecordMonitoringSuccess()

	if !reg.MonitoringStale(0) {
		t.Error("MonitoringStale should be true once elapsed time exceeds a zero threshold")
	}
}
