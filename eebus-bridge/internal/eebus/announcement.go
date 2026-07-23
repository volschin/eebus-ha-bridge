package eebus

import (
	"errors"
	"fmt"

	"github.com/enbility/eebus-go/features/server"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

var (
	errNoLocalEntity      = errors.New("local entity not available")
	errNoManufacturerData = errors.New("local manufacturer data not available")
)

// AnnounceLocalIdentity fills the two gaps spine-go leaves in the local device's
// SPINE announcement. Call it after BridgeService.Setup (which creates the local
// device) and before Start, so the data is in place before the first remote reads
// it.
//
// vendor is the display name for deviceClassificationManufacturerData. Passing an
// empty string keeps whatever spine-go derived from the brand.
//
// Both halves are attempted even when the first fails, so a partial announcement
// still gets as far as it can and the caller sees every reason it did not.
func (b *BridgeService) AnnounceLocalIdentity(vendor string) error {
	return errors.Join(
		b.SetOperatingState(model.DeviceDiagnosisOperatingStateTypeNormalOperation),
		b.setVendorName(vendor),
	)
}

// SetOperatingState publishes the local entity's DeviceDiagnosis operating state.
//
// spine-go creates the DeviceDiagnosis server feature for the heartbeat but never
// publishes deviceDiagnosisStateData, so a remote reading our operating state got
// an empty response. The state lives on the CEM entity — the functional one, the
// same place a Vaillant gateway puts it on its HeatPumpAppliance entity — not on
// the DeviceInformation entity.
func (b *BridgeService) SetOperatingState(state model.DeviceDiagnosisOperatingStateType) error {
	return setLocalOperatingState(b.LocalEntity(), state)
}

// setVendorName corrects the vendor name in deviceClassificationManufacturerData.
//
// spine-go fills both BrandName and VendorName from the brand it was constructed
// with (spine/device_local.go), so the configured eebus.vendor never reached the
// wire and the bridge announced its brand twice. Everything else in the payload
// stays as spine-go built it.
func (b *BridgeService) setVendorName(vendor string) error {
	if vendor == "" {
		return nil
	}
	var entity spineapi.EntityLocalInterface
	if device := b.service.LocalDevice(); device != nil {
		entity = device.EntityForType(model.EntityTypeTypeDeviceInformation)
	}
	return setLocalVendorName(entity, vendor)
}

// setLocalOperatingState is the entity-scoped half of SetOperatingState, split out
// so the failure paths are reachable without a running EEBUS service.
func setLocalOperatingState(
	entity spineapi.EntityLocalInterface,
	state model.DeviceDiagnosisOperatingStateType,
) error {
	if entity == nil {
		return errNoLocalEntity
	}

	// GetOrAddFeature and AddFunctionType are both idempotent, so this stays
	// correct when the heartbeat manager has already created the feature.
	feature := entity.GetOrAddFeature(model.FeatureTypeTypeDeviceDiagnosis, model.RoleTypeServer)
	if feature == nil {
		return errNoLocalEntity
	}
	feature.AddFunctionType(model.FunctionTypeDeviceDiagnosisStateData, true, false)

	diagnosis, err := server.NewDeviceDiagnosis(entity)
	if err != nil {
		return fmt.Errorf("creating local device diagnosis: %w", err)
	}
	diagnosis.SetLocalOperatingState(state)
	return nil
}

// setLocalVendorName is the entity-scoped half of setVendorName, split out for the
// same reason.
func setLocalVendorName(entity spineapi.EntityLocalInterface, vendor string) error {
	if entity == nil {
		return errNoLocalEntity
	}
	feature := entity.FeatureOfTypeAndRole(model.FeatureTypeTypeDeviceClassification, model.RoleTypeServer)
	if feature == nil {
		return errNoManufacturerData
	}

	data, ok := feature.DataCopy(model.FunctionTypeDeviceClassificationManufacturerData).(*model.DeviceClassificationManufacturerDataType)
	if !ok || data == nil {
		return errNoManufacturerData
	}
	name := model.DeviceClassificationStringType(vendor)
	data.VendorName = &name
	feature.SetData(model.FunctionTypeDeviceClassificationManufacturerData, data)
	return nil
}
