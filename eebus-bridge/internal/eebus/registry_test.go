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

func TestRegistryListDevices(t *testing.T) {
	reg := eebus.NewDeviceRegistry()
	reg.AddDevice("ski-1", eebus.DeviceInfo{Brand: "A"})
	reg.AddDevice("ski-2", eebus.DeviceInfo{Brand: "B"})

	devices := reg.ListDevices()
	if len(devices) != 2 {
		t.Errorf("len(devices) = %d, want 2", len(devices))
	}
}
