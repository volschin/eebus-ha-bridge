package usecases

import (
	"errors"
	"testing"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

func classificationValue(value string) *model.DeviceClassificationStringType {
	result := model.DeviceClassificationStringType(value)
	return &result
}

func TestDeviceClassifierSetupAddsClientFeature(t *testing.T) {
	classifier := NewDeviceClassifier(eebus.NewDeviceRegistry(), eebus.NewEventBus())
	if err := classifier.Setup(nil); err == nil {
		t.Fatal("Setup(nil) succeeded")
	}
	local := clientUsecaseLocalEntity(t)
	if err := classifier.Setup(local); err != nil {
		t.Fatal(err)
	}
	if local.FeatureOfTypeAndRole(model.FeatureTypeTypeDeviceClassification, model.RoleTypeClient) == nil {
		t.Fatal("DeviceClassification client feature was not added")
	}
}

func TestDeviceClassifierSetupReportsFeatureAndSubscriptionFailures(t *testing.T) {
	classifier := NewDeviceClassifier(eebus.NewDeviceRegistry(), nil)
	missingFeature := mocks.NewEntityLocalInterface(t)
	missingFeature.On("GetOrAddFeature", model.FeatureTypeTypeDeviceClassification, model.RoleTypeClient).
		Return(nil)
	if err := classifier.Setup(missingFeature); err == nil {
		t.Fatal("missing client feature did not fail setup")
	}

	local := mocks.NewEntityLocalInterface(t)
	localFeature := mocks.NewFeatureLocalInterface(t)
	device := mocks.NewDeviceLocalInterface(t)
	events := mocks.NewEventsManagerInterface(t)
	local.On("GetOrAddFeature", model.FeatureTypeTypeDeviceClassification, model.RoleTypeClient).
		Return(localFeature)
	local.On("Device").Return(device)
	device.On("Events").Return(events)
	events.On("Subscribe", classifier).Return(errors.New("subscribe failed"))
	if err := classifier.Setup(local); err == nil {
		t.Fatal("subscription failure did not fail setup")
	}
}

func TestDeviceClassifierStoresManufacturerDataAndPublishesChange(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	bus := eebus.NewEventBus()
	events := bus.Subscribe()
	defer bus.Unsubscribe(events)
	classifier := NewDeviceClassifier(registry, bus)
	classifier.localEntity = mocks.NewEntityLocalInterface(t)
	device := mocks.NewDeviceRemoteInterface(t)
	deviceType := model.DeviceTypeTypeHeatgenerationSystem
	device.On("DeviceType").Return(&deviceType)
	data := &model.DeviceClassificationManufacturerDataType{
		BrandName:        classificationValue("Vaillant"),
		DeviceCode:       classificationValue("VR940"),
		SerialNumber:     classificationValue("SN-1"),
		SoftwareRevision: classificationValue("4.2.1"),
		HardwareRevision: classificationValue("R3"),
	}
	payload := spineapi.EventPayload{Ski: testValidUsecaseSKI, Device: device, Data: data}

	classifier.HandleEvent(payload)
	info, ok := registry.GetDevice(testValidUsecaseSKI)
	if !ok || info.Brand != "Vaillant" || info.Model != "VR940" || info.Serial != "SN-1" ||
		info.SoftwareRevision != "4.2.1" || info.HardwareRevision != "R3" ||
		info.DeviceType != string(deviceType) {
		t.Fatalf("classification = %+v, ok=%t", info, ok)
	}
	if event := <-events; event.Type != eebus.EventTypeDeviceClassificationUpdated {
		t.Fatalf("event = %+v", event)
	}

	classifier.HandleEvent(payload)
	select {
	case event := <-events:
		t.Fatalf("unchanged classification published %+v", event)
	default:
	}
}

func TestDeviceClassifierReadsCachedClassificationOnEntityDiscovery(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	classifier := NewDeviceClassifier(registry, nil)
	local := mocks.NewEntityLocalInterface(t)
	localFeature := mocks.NewFeatureLocalInterface(t)
	local.On("Device").Return(nil)
	local.On("FeatureOfTypeAndRole", model.FeatureTypeTypeDeviceClassification, model.RoleTypeClient).
		Return(localFeature)
	classifier.localEntity = local

	device := mocks.NewDeviceRemoteInterface(t)
	entity := mocks.NewEntityRemoteInterface(t)
	remoteFeature := mocks.NewFeatureRemoteInterface(t)
	remoteFeature.On("String").Return("DeviceClassification/server").Maybe()
	deviceType := model.DeviceTypeTypeEnergyManagementSystem
	device.On("DeviceType").Return(&deviceType)
	device.On(
		"FeatureByEntityTypeAndRole",
		entity,
		model.FeatureTypeTypeDeviceClassification,
		model.RoleTypeServer,
	).Return(remoteFeature)
	entity.On("Device").Return(device)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeDeviceClassification, model.RoleTypeServer).
		Return(remoteFeature)
	remoteFeature.On("DataCopy", model.FunctionTypeDeviceClassificationManufacturerData).Return(
		&model.DeviceClassificationManufacturerDataType{
			BrandName:  classificationValue("Vaillant"),
			DeviceName: classificationValue("Gateway"),
		},
	)

	classifier.HandleEvent(spineapi.EventPayload{
		Ski: testValidUsecaseSKI, Device: device, Entity: entity,
		EventType: spineapi.EventTypeEntityChange, ChangeType: spineapi.ElementChangeAdd,
	})
	info, ok := registry.GetDevice(testValidUsecaseSKI)
	if !ok || info.Brand != "Vaillant" || info.Model != "Gateway" {
		t.Fatalf("cached classification = %+v, ok=%t", info, ok)
	}
	if details := manufacturerDetails(local, nil, entity); details.brand != "Vaillant" || details.model != "Gateway" {
		t.Fatalf("manufacturer details = %+v", details)
	}
}

func TestDeviceClassifierRequestsMissingClassification(t *testing.T) {
	classifier := NewDeviceClassifier(eebus.NewDeviceRegistry(), nil)
	local := mocks.NewEntityLocalInterface(t)
	localFeature := mocks.NewFeatureLocalInterface(t)
	local.On("Device").Return(nil)
	local.On("FeatureOfTypeAndRole", model.FeatureTypeTypeDeviceClassification, model.RoleTypeClient).
		Return(localFeature)
	classifier.localEntity = local

	device := mocks.NewDeviceRemoteInterface(t)
	entity := mocks.NewEntityRemoteInterface(t)
	remoteFeature := mocks.NewFeatureRemoteInterface(t)
	remoteFeature.On("String").Return("DeviceClassification/server").Maybe()
	operation := mocks.NewOperationsInterface(t)
	operation.On("Read").Return(true)
	entity.On("Device").Return(device)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeDeviceClassification, model.RoleTypeServer).
		Return(remoteFeature)
	device.On(
		"FeatureByEntityTypeAndRole",
		entity,
		model.FeatureTypeTypeDeviceClassification,
		model.RoleTypeServer,
	).Return(remoteFeature)
	remoteFeature.On("DataCopy", model.FunctionTypeDeviceClassificationManufacturerData).Return(nil)
	remoteFeature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{
		model.FunctionTypeDeviceClassificationManufacturerData: operation,
	})
	counter := model.MsgCounterType(1)
	localFeature.On(
		"RequestRemoteData",
		model.FunctionTypeDeviceClassificationManufacturerData,
		nil,
		nil,
		remoteFeature,
	).Return(&counter, (*model.ErrorType)(nil))

	classifier.HandleEvent(spineapi.EventPayload{
		Ski: testValidUsecaseSKI, Device: device, Entity: entity,
		EventType: spineapi.EventTypeEntityChange, ChangeType: spineapi.ElementChangeAdd,
	})
}

func TestDeviceClassifierIgnoresIrrelevantAndUnavailableEntities(t *testing.T) {
	var nilClassifier *DeviceClassifier
	nilClassifier.HandleEvent(spineapi.EventPayload{})
	classifier := NewDeviceClassifier(eebus.NewDeviceRegistry(), nil)
	classifier.HandleEvent(spineapi.EventPayload{})
	classifier.localEntity = mocks.NewEntityLocalInterface(t)
	classifier.HandleEvent(spineapi.EventPayload{ChangeType: spineapi.ElementChangeUpdate})

	device := mocks.NewDeviceRemoteInterface(t)
	device.On("Entities").Return([]spineapi.EntityRemoteInterface{nil})
	classifier.HandleEvent(spineapi.EventPayload{
		Device: device, EventType: spineapi.EventTypeDeviceChange, ChangeType: spineapi.ElementChangeAdd,
	})

	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeDeviceClassification, model.RoleTypeServer).
		Return(nil)
	classifier.readOrRequest(testValidUsecaseSKI, nil, entity)
}

func TestDeviceClassifierHandlesMissingClientAndReadOperation(t *testing.T) {
	classifier := NewDeviceClassifier(eebus.NewDeviceRegistry(), nil)
	local := mocks.NewEntityLocalInterface(t)
	local.On("Device").Return(nil)
	local.On("FeatureOfTypeAndRole", model.FeatureTypeTypeDeviceClassification, model.RoleTypeClient).
		Return(nil)
	local.On("FeatureOfTypeAndRole", model.FeatureTypeTypeGeneric, model.RoleTypeClient).Return(nil)
	classifier.localEntity = local
	device := mocks.NewDeviceRemoteInterface(t)
	entity := mocks.NewEntityRemoteInterface(t)
	remoteFeature := mocks.NewFeatureRemoteInterface(t)
	remoteFeature.On("String").Return("DeviceClassification/server").Maybe()
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeDeviceClassification, model.RoleTypeServer).
		Return(remoteFeature)
	entity.On("Device").Return(device)
	device.On(
		"FeatureByEntityTypeAndRole",
		entity,
		model.FeatureTypeTypeDeviceClassification,
		model.RoleTypeServer,
	).Return(remoteFeature).Maybe()
	classifier.readOrRequest(testValidUsecaseSKI, device, entity)

	localWithFeature := mocks.NewEntityLocalInterface(t)
	localFeature := mocks.NewFeatureLocalInterface(t)
	localWithFeature.On("Device").Return(nil)
	localWithFeature.On("FeatureOfTypeAndRole", model.FeatureTypeTypeDeviceClassification, model.RoleTypeClient).
		Return(localFeature)
	classifier.localEntity = localWithFeature
	remoteFeature.On("DataCopy", model.FunctionTypeDeviceClassificationManufacturerData).Return(nil)
	remoteFeature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{})
	classifier.readOrRequest(testValidUsecaseSKI, device, entity)
}

func TestEnrichDeviceClassificationGuardsAndStoresDeviceType(t *testing.T) {
	enrichDeviceClassification(nil, nil, "", nil, nil)
	registry := eebus.NewDeviceRegistry()
	enrichDeviceClassification(registry, nil, testValidUsecaseSKI, nil, nil)
	if _, ok := registry.GetDevice(testValidUsecaseSKI); ok {
		t.Fatal("empty fallback classification created a device")
	}
	device := mocks.NewDeviceRemoteInterface(t)
	deviceType := model.DeviceTypeTypeHeatgenerationSystem
	device.On("DeviceType").Return(&deviceType)
	enrichDeviceClassification(registry, nil, testValidUsecaseSKI, device, nil)
	info, ok := registry.GetDevice(testValidUsecaseSKI)
	if !ok || info.DeviceType != string(deviceType) {
		t.Fatalf("device type classification = %+v, ok=%t", info, ok)
	}
	if manufacturerDetails(nil, device, nil) != (deviceClassificationDetails{}) {
		t.Fatal("nil local entity returned manufacturer details")
	}
}

func TestClassificationDetailsPreferDeviceCodeAndMergePartialValues(t *testing.T) {
	details := classificationDetails(&model.DeviceClassificationManufacturerDataType{
		DeviceName: classificationValue("name"), DeviceCode: classificationValue("code"),
	})
	if details.model != "code" {
		t.Fatalf("model = %q", details.model)
	}
	mergeClassificationDetails(&details, deviceClassificationDetails{
		brand: "Vaillant", model: "replacement", serial: "SN", softwareRevision: "SW", hardwareRevision: "HW",
	})
	if details.brand != "Vaillant" || details.model != "code" || details.serial != "SN" ||
		details.softwareRevision != "SW" || details.hardwareRevision != "HW" {
		t.Fatalf("merged details = %+v", details)
	}
	if classificationDetails(nil) != (deviceClassificationDetails{}) || classificationString(nil) != "" {
		t.Fatal("nil classification values were not empty")
	}
	vendorFallback := classificationDetails(&model.DeviceClassificationManufacturerDataType{
		VendorName: classificationValue("Vaillant Group"),
	})
	if vendorFallback.brand != "Vaillant Group" {
		t.Fatalf("vendor fallback brand = %q", vendorFallback.brand)
	}
}
