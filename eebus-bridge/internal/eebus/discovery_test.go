package eebus

import (
	"strings"
	"sync"
	"testing"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
)

// buildDeviceMock returns a remote-device mock advertising one entity and one use
// case, mirroring what a Vaillant gateway reports after discovery completes.
func buildDeviceMock(t *testing.T, ski string) *mocks.DeviceRemoteInterface {
	t.Helper()

	deviceType := model.DeviceTypeTypeHeatgenerationSystem
	entityType := model.EntityTypeTypeCompressor
	entityAddr := &model.EntityAddressType{Entity: []model.AddressEntityType{1, 2}}

	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("Type").Return(model.FeatureTypeTypeMeasurement).Maybe()
	feature.On("Role").Return(model.RoleTypeServer).Maybe()

	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("Address").Return(entityAddr).Maybe()
	entity.On("EntityType").Return(entityType).Maybe()
	entity.On("Features").Return([]spineapi.FeatureRemoteInterface{feature}).Maybe()

	device := mocks.NewDeviceRemoteInterface(t)
	device.On("Ski").Return(ski).Maybe()
	device.On("DeviceType").Return(&deviceType).Maybe()
	device.On("Entities").Return([]spineapi.EntityRemoteInterface{entity}).Maybe()

	actor := model.UseCaseActorTypeMonitoringAppliance
	ucName := model.UseCaseNameTypeMonitoringOfPowerConsumption
	ucVersion := model.SpecificationVersionType("1.0.0")
	available := true
	device.On("UseCases").Return([]model.UseCaseInformationDataType{
		{
			Actor: &actor,
			UseCaseSupport: []model.UseCaseSupportType{
				{
					UseCaseName:      &ucName,
					UseCaseVersion:   &ucVersion,
					UseCaseAvailable: &available,
					ScenarioSupport:  []model.UseCaseScenarioSupportType{1, 2, 3},
				},
			},
		},
	}).Maybe()

	return device
}

func TestFormatDeviceUseCases(t *testing.T) {
	device := buildDeviceMock(t, "ABCD1234")
	out := FormatDeviceUseCases("ABCD1234", device)

	for _, want := range []string{
		"[DISCOVERY] device ski=ABCD1234",
		string(model.DeviceTypeTypeHeatgenerationSystem),
		string(model.UseCaseNameTypeMonitoringOfPowerConsumption),
		"available=true",
		"scenarios=[1,2,3]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatDeviceUseCases output missing %q:\n%s", want, out)
		}
	}
}

func TestUseCaseDiscoveryLogOnceDedup(t *testing.T) {
	var (
		mu    sync.Mutex
		calls int
	)
	d := NewUseCaseDiscovery(func(string, ...any) {
		mu.Lock()
		calls++
		mu.Unlock()
	})

	device := buildDeviceMock(t, "ABCD1234")

	d.LogOnce("ABCD1234", device)
	d.LogOnce("abcd1234", device)   // same SKI, different case -> deduped
	d.LogOnce("  abcd 1234 ", device) // same SKI, spacing -> deduped

	if calls != 1 {
		t.Errorf("LogOnce logged %d times, want 1 (dedup by normalized SKI)", calls)
	}
}

func TestUseCaseDiscoveryLogOnceNilDevice(t *testing.T) {
	d := NewUseCaseDiscovery(func(string, ...any) {
		t.Fatal("logf must not be called for a nil device")
	})
	d.LogOnce("ABCD1234", nil)
}
