package eebus

import (
	"errors"
	"strings"
	"testing"

	shipcert "github.com/enbility/ship-go/cert"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/config"
)

// newAnnouncementBridge builds a real, set-up bridge service so the assertions run
// against the data spine-go actually holds for the local device.
func newAnnouncementBridge(t *testing.T, vendor string, port int) *BridgeService {
	t.Helper()

	certificate, err := shipcert.CreateCertificate("test", "test", "DE", "announcement")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		EEBUS: config.EEBUSConfig{
			Port:   port,
			Vendor: vendor,
			Brand:  "test-brand",
			Model:  "test-model",
			Serial: "announcement",
		},
	}
	bridge, err := NewBridgeService(cfg, certificate, NewEventBus())
	if err != nil {
		t.Fatal(err)
	}
	if err := bridge.Setup(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Shutdown)
	return bridge
}

func TestAnnounceLocalIdentityPublishesOperatingState(t *testing.T) {
	bridge := newAnnouncementBridge(t, "test-vendor", 49881)

	if err := bridge.AnnounceLocalIdentity("test-vendor"); err != nil {
		t.Fatalf("AnnounceLocalIdentity: %v", err)
	}

	feature := bridge.LocalEntity().FeatureOfTypeAndRole(
		model.FeatureTypeTypeDeviceDiagnosis, model.RoleTypeServer)
	if feature == nil {
		t.Fatal("local entity has no DeviceDiagnosis server feature")
	}
	if operations := feature.Operations()[model.FunctionTypeDeviceDiagnosisStateData]; operations == nil ||
		!operations.Read() {
		t.Fatal("deviceDiagnosisStateData is not announced as readable")
	}
	data, ok := feature.DataCopy(model.FunctionTypeDeviceDiagnosisStateData).(*model.DeviceDiagnosisStateDataType)
	if !ok || data == nil || data.OperatingState == nil {
		t.Fatalf("operating state not published: %#v", data)
	}
	if *data.OperatingState != model.DeviceDiagnosisOperatingStateTypeNormalOperation {
		t.Fatalf("operating state = %s, want %s",
			*data.OperatingState, model.DeviceDiagnosisOperatingStateTypeNormalOperation)
	}
}

func TestAnnounceLocalIdentitySetsVendorName(t *testing.T) {
	bridge := newAnnouncementBridge(t, "test-vendor", 49882)

	if err := bridge.AnnounceLocalIdentity("test-vendor"); err != nil {
		t.Fatalf("AnnounceLocalIdentity: %v", err)
	}

	data := manufacturerData(t, bridge)
	if data.VendorName == nil || string(*data.VendorName) != "test-vendor" {
		t.Fatalf("vendor name = %v, want test-vendor", data.VendorName)
	}
	// The rest of the payload must survive the rewrite.
	if data.BrandName == nil || string(*data.BrandName) != "test-brand" {
		t.Fatalf("brand name = %v, want test-brand", data.BrandName)
	}
	if data.SerialNumber == nil || string(*data.SerialNumber) != "announcement" {
		t.Fatalf("serial number = %v, want announcement", data.SerialNumber)
	}
}

// TestAnnounceLocalIdentityKeepsBrandVendorWithoutConfig covers the empty-vendor
// argument: spine-go's brand-derived vendor name stays untouched. (eebus-go
// rejects an empty vendorCode at construction, so this is the defensive path, not
// a reachable configuration.)
func TestAnnounceLocalIdentityKeepsBrandVendorWithoutConfig(t *testing.T) {
	bridge := newAnnouncementBridge(t, "test-vendor", 49883)

	if err := bridge.AnnounceLocalIdentity(""); err != nil {
		t.Fatalf("AnnounceLocalIdentity: %v", err)
	}

	data := manufacturerData(t, bridge)
	if data.VendorName == nil || string(*data.VendorName) != "test-brand" {
		t.Fatalf("vendor name = %v, want the brand-derived test-brand", data.VendorName)
	}
}

// TestSetOperatingStateIsIdempotent covers the second call: the feature and its
// function type already exist, and the state is simply overwritten. This is the
// path a later failure/standby transition would take.
func TestSetOperatingStateIsIdempotent(t *testing.T) {
	bridge := newAnnouncementBridge(t, "test-vendor", 49884)

	if err := bridge.SetOperatingState(model.DeviceDiagnosisOperatingStateTypeNormalOperation); err != nil {
		t.Fatalf("first SetOperatingState: %v", err)
	}
	if err := bridge.SetOperatingState(model.DeviceDiagnosisOperatingStateTypeFailure); err != nil {
		t.Fatalf("second SetOperatingState: %v", err)
	}

	feature := bridge.LocalEntity().FeatureOfTypeAndRole(
		model.FeatureTypeTypeDeviceDiagnosis, model.RoleTypeServer)
	data, ok := feature.DataCopy(model.FunctionTypeDeviceDiagnosisStateData).(*model.DeviceDiagnosisStateDataType)
	if !ok || data == nil || data.OperatingState == nil {
		t.Fatalf("operating state not published: %#v", data)
	}
	if *data.OperatingState != model.DeviceDiagnosisOperatingStateTypeFailure {
		t.Fatalf("operating state = %s, want %s",
			*data.OperatingState, model.DeviceDiagnosisOperatingStateTypeFailure)
	}
}

func manufacturerData(t *testing.T, bridge *BridgeService) *model.DeviceClassificationManufacturerDataType {
	t.Helper()

	entity := bridge.Service().LocalDevice().EntityForType(model.EntityTypeTypeDeviceInformation)
	if entity == nil {
		t.Fatal("local device has no DeviceInformation entity")
	}
	feature := entity.FeatureOfTypeAndRole(model.FeatureTypeTypeDeviceClassification, model.RoleTypeServer)
	if feature == nil {
		t.Fatal("DeviceInformation entity has no DeviceClassification server feature")
	}
	data, ok := feature.DataCopy(
		model.FunctionTypeDeviceClassificationManufacturerData).(*model.DeviceClassificationManufacturerDataType)
	if !ok || data == nil {
		t.Fatal("manufacturer data unavailable")
	}
	return data
}

// TestSetLocalOperatingStateFailurePaths covers the guards that a running service
// never hits: a missing entity, an entity that yields no feature, and the
// eebus-go constructor failing because the feature is not resolvable.
func TestSetLocalOperatingStateFailurePaths(t *testing.T) {
	state := model.DeviceDiagnosisOperatingStateTypeNormalOperation

	if err := setLocalOperatingState(nil, state); !errors.Is(err, errNoLocalEntity) {
		t.Fatalf("nil entity: err = %v, want %v", err, errNoLocalEntity)
	}

	noFeature := mocks.NewEntityLocalInterface(t)
	noFeature.On("GetOrAddFeature", model.FeatureTypeTypeDeviceDiagnosis, model.RoleTypeServer).
		Return(nil).Once()
	if err := setLocalOperatingState(noFeature, state); !errors.Is(err, errNoLocalEntity) {
		t.Fatalf("nil feature: err = %v, want %v", err, errNoLocalEntity)
	}

	feature := mocks.NewFeatureLocalInterface(t)
	feature.On("AddFunctionType", model.FunctionTypeDeviceDiagnosisStateData, true, false).Once()
	unresolvable := mocks.NewEntityLocalInterface(t)
	unresolvable.On("GetOrAddFeature", model.FeatureTypeTypeDeviceDiagnosis, model.RoleTypeServer).
		Return(feature).Once()
	unresolvable.On("Device").Return(nil).Once()
	unresolvable.On("FeatureOfTypeAndRole", model.FeatureTypeTypeDeviceDiagnosis, model.RoleTypeServer).
		Return(nil).Once()
	err := setLocalOperatingState(unresolvable, state)
	if err == nil || !strings.Contains(err.Error(), "creating local device diagnosis") {
		t.Fatalf("unresolvable feature: err = %v", err)
	}
}

// TestSetLocalVendorNameFailurePaths covers the same class of guards for the
// manufacturer-data rewrite.
func TestSetLocalVendorNameFailurePaths(t *testing.T) {
	if err := setLocalVendorName(nil, "vendor"); !errors.Is(err, errNoLocalEntity) {
		t.Fatalf("nil entity: err = %v, want %v", err, errNoLocalEntity)
	}

	noFeature := mocks.NewEntityLocalInterface(t)
	noFeature.On("FeatureOfTypeAndRole", model.FeatureTypeTypeDeviceClassification, model.RoleTypeServer).
		Return(nil).Once()
	if err := setLocalVendorName(noFeature, "vendor"); !errors.Is(err, errNoManufacturerData) {
		t.Fatalf("nil feature: err = %v, want %v", err, errNoManufacturerData)
	}

	feature := mocks.NewFeatureLocalInterface(t)
	feature.On("DataCopy", model.FunctionTypeDeviceClassificationManufacturerData).Return(nil).Once()
	noData := mocks.NewEntityLocalInterface(t)
	noData.On("FeatureOfTypeAndRole", model.FeatureTypeTypeDeviceClassification, model.RoleTypeServer).
		Return(feature).Once()
	if err := setLocalVendorName(noData, "vendor"); !errors.Is(err, errNoManufacturerData) {
		t.Fatalf("missing manufacturer data: err = %v, want %v", err, errNoManufacturerData)
	}
}
