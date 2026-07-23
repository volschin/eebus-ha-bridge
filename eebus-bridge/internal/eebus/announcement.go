package eebus

import (
	"errors"
	"fmt"

	"github.com/enbility/eebus-go/features/server"
	"github.com/enbility/spine-go/model"
)

var errNoLocalEntity = errors.New("local entity not available")

// AnnounceLocalIdentity fills the two gaps spine-go leaves in the local device's
// SPINE announcement. Call it after BridgeService.Setup (which creates the local
// device) and before Start, so the data is in place before the first remote reads
// it.
//
// vendor is the display name for deviceClassificationManufacturerData. Passing an
// empty string keeps whatever spine-go derived from the brand.
func (b *BridgeService) AnnounceLocalIdentity(vendor string) error {
	if err := b.SetOperatingState(model.DeviceDiagnosisOperatingStateTypeNormalOperation); err != nil {
		return err
	}
	return b.setVendorName(vendor)
}

// SetOperatingState publishes the local entity's DeviceDiagnosis operating state.
//
// spine-go creates the DeviceDiagnosis server feature for the heartbeat but never
// publishes deviceDiagnosisStateData, so a remote reading our operating state got
// an empty response. The state lives on the CEM entity — the functional one, the
// same place a Vaillant gateway puts it on its HeatPumpAppliance entity — not on
// the DeviceInformation entity.
func (b *BridgeService) SetOperatingState(state model.DeviceDiagnosisOperatingStateType) error {
	entity := b.LocalEntity()
	if entity == nil {
		return errNoLocalEntity
	}

	// GetOrAddFeature and AddFunctionType are both idempotent, so this stays
	// correct when the heartbeat manager has already created the feature.
	feature := entity.GetOrAddFeature(model.FeatureTypeTypeDeviceDiagnosis, model.RoleTypeServer)
	feature.AddFunctionType(model.FunctionTypeDeviceDiagnosisStateData, true, false)

	diagnosis, err := server.NewDeviceDiagnosis(entity)
	if err != nil {
		return fmt.Errorf("creating local device diagnosis: %w", err)
	}
	diagnosis.SetLocalOperatingState(state)
	return nil
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

	device := b.service.LocalDevice()
	if device == nil {
		return errNoLocalEntity
	}
	entity := device.EntityForType(model.EntityTypeTypeDeviceInformation)
	if entity == nil {
		return errNoLocalEntity
	}
	feature := entity.FeatureOfTypeAndRole(model.FeatureTypeTypeDeviceClassification, model.RoleTypeServer)
	if feature == nil {
		return errNoLocalEntity
	}

	data, ok := feature.DataCopy(model.FunctionTypeDeviceClassificationManufacturerData).(*model.DeviceClassificationManufacturerDataType)
	if !ok || data == nil {
		return fmt.Errorf("local manufacturer data unavailable")
	}
	name := model.DeviceClassificationStringType(vendor)
	data.VendorName = &name
	feature.SetData(model.FunctionTypeDeviceClassificationManufacturerData, data)
	return nil
}
