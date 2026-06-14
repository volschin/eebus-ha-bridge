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
	reg.AddDevice("ski-1", eebus.DeviceInfo{Brand: "A"})
	reg.AddDevice("ski-2", eebus.DeviceInfo{Brand: "B"})

	devices := reg.ListDevices()
	if len(devices) != 2 {
		t.Errorf("len(devices) = %d, want 2", len(devices))
	}
}
