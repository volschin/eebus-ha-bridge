package eebus_test

import (
	"testing"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestRegistryTrustHealthAndMonitoringSuccessProjection(t *testing.T) {
	start := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: start}
	registry := eebus.NewDeviceRegistryWithClock(clock)

	if _, ok := registry.DeviceHealth("ab:cd"); ok {
		t.Fatal("unknown device returned a health projection")
	}
	if registry.MonitoringSuccessSince("ab:cd", start.Add(-time.Hour)) {
		t.Fatal("unknown device reported a monitoring success")
	}

	registry.MarkTrusted("ab:cd")
	health, ok := registry.DeviceHealth("ABCD")
	if !ok {
		t.Fatal("trusted device has no health projection")
	}
	if !health.TrustKnown || !health.Trusted || health.Connected {
		t.Fatalf("health after MarkTrusted = %+v", health)
	}
	if health.LastTransitionAt != start {
		t.Fatalf("transition = %v, want %v", health.LastTransitionAt, start)
	}
	if !registry.KnownDevice("ab-cd") {
		t.Fatal("health-only device was not considered known")
	}

	clock.Advance(time.Minute)
	registry.MarkConnected("ABCD")
	clock.Advance(time.Minute)
	since := clock.Now().Add(-time.Second)
	registry.RecordMonitoringSuccess("ab cd")

	health, ok = registry.DeviceHealth("abcd")
	if !ok || !health.Connected || !health.MonitoringSuccessOnConnect {
		t.Fatalf("health after monitoring success = %+v, ok=%t", health, ok)
	}
	if health.LastMonitoringSuccess != clock.Now() {
		t.Fatalf("last success = %v, want %v", health.LastMonitoringSuccess, clock.Now())
	}
	if !registry.MonitoringSuccessSince("ABCD", since) {
		t.Fatal("fresh monitoring success was not detected")
	}
	if registry.MonitoringSuccessSince("ABCD", clock.Now()) {
		t.Fatal("success timestamp should be strictly after the comparison timestamp")
	}
}

func TestRegistryUpsertAndRemoveObservationContract(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("Type").Return(model.FeatureTypeTypeMeasurement)
	feature.On("Role").Return(model.RoleTypeServer)

	duplicateFeature := mocks.NewFeatureRemoteInterface(t)
	duplicateFeature.On("Type").Return(model.FeatureTypeTypeMeasurement)
	duplicateFeature.On("Role").Return(model.RoleTypeServer)

	address := &model.EntityAddressType{Entity: []model.AddressEntityType{1, 2}}
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("Address").Return(address)
	entity.On("EntityType").Return(model.EntityTypeTypeCompressor)
	entity.On("Features").Return([]spineapi.FeatureRemoteInterface{nil, feature, duplicateFeature})

	device := mocks.NewDeviceRemoteInterface(t)
	device.On("Ski").Return("ab:cd")

	registry.UpsertObservation("", device, entity, "monitoring")
	registry.UpsertObservation("ABCD", device, entity, "monitoring")

	info, ok := registry.GetDevice("ab-cd")
	if !ok {
		t.Fatal("observation did not create a device")
	}
	if info.SKI != "ABCD" || info.RemoteDevice != device {
		t.Fatalf("device info = %+v", info)
	}
	if len(info.UseCases) != 1 || info.UseCases[0] != "monitoring" {
		t.Fatalf("use cases = %v, want one monitoring entry", info.UseCases)
	}
	if len(info.RemoteEntities) != 1 || info.RemoteEntities[0] != entity {
		t.Fatalf("remote entities = %v, want one deduplicated entity", info.RemoteEntities)
	}
	if len(info.Entities) != 1 {
		t.Fatalf("entities = %+v, want one", info.Entities)
	}
	entityInfo := info.Entities[0]
	if entityInfo.Address != "1:2" || entityInfo.Type != string(model.EntityTypeTypeCompressor) {
		t.Fatalf("entity info = %+v", entityInfo)
	}
	if len(entityInfo.Features) != 1 || entityInfo.Features[0] != "Measurement/server" {
		t.Fatalf("features = %v, want deduplicated Measurement/server", entityInfo.Features)
	}
	if got := registry.FirstEntityForType("ABCD", string(model.EntityTypeTypeCompressor)); got != entity {
		t.Fatalf("FirstEntityForType = %v, want observed entity", got)
	}
	if got := registry.FirstEntityForType("ABCD", string(model.EntityTypeTypeCEM)); got != nil {
		t.Fatalf("FirstEntityForType for missing type = %v, want nil", got)
	}

	registry.RemoveEntityObservation("ABCD", nil)
	registry.RemoveEntityObservation("unknown", entity)
	registry.RemoveEntityObservation("ab:cd", entity)
	info, ok = registry.GetDevice("ABCD")
	if !ok || len(info.RemoteEntities) != 0 || len(info.Entities) != 0 {
		t.Fatalf("device after entity removal = %+v, ok=%t", info, ok)
	}
}

func TestRegistryAddressAndFeatureFormatting(t *testing.T) {
	if got := eebus.EntityAddressString(nil); got != "" {
		t.Fatalf("nil address = %q", got)
	}
	if got := eebus.EntityAddressString(&model.EntityAddressType{}); got != "" {
		t.Fatalf("empty address = %q", got)
	}
	address := &model.EntityAddressType{Entity: []model.AddressEntityType{0, 7, 42}}
	if got := eebus.EntityAddressString(address); got != "0:7:42" {
		t.Fatalf("address = %q, want 0:7:42", got)
	}

	if got := eebus.FeatureStrings(nil); len(got) != 0 {
		t.Fatalf("nil features = %v", got)
	}
	client := mocks.NewFeatureRemoteInterface(t)
	client.On("Type").Return(model.FeatureTypeTypeMeasurement)
	client.On("Role").Return(model.RoleTypeClient)
	server := mocks.NewFeatureRemoteInterface(t)
	server.On("Type").Return(model.FeatureTypeTypeMeasurement)
	server.On("Role").Return(model.RoleTypeServer)
	got := eebus.FeatureStrings([]spineapi.FeatureRemoteInterface{nil, client, server, client})
	want := []string{"Measurement/client", "Measurement/server"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("features = %v, want %v", got, want)
	}
}

func TestRegistryKnownDeviceFromCatalogAndUnknownEntityType(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	if registry.KnownDevice("ab:cd") {
		t.Fatal("empty registry considered device known")
	}
	registry.AddDevice("ab:cd", eebus.DeviceInfo{Brand: "Brand"})
	if !registry.KnownDevice("ABCD") {
		t.Fatal("catalog device was not considered known")
	}
	if got := registry.FirstEntityForType("unknown", "Compressor"); got != nil {
		t.Fatalf("unknown device entity = %v, want nil", got)
	}
}
