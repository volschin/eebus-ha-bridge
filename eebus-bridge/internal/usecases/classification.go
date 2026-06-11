package usecases

import (
	"github.com/enbility/eebus-go/features/client"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

// enrichDeviceClassification reads brand, model and serial from the remote
// device's DeviceClassification manufacturer data and the EEBUS device type,
// then stores them in the registry. It is best-effort: any missing data leaves
// the corresponding registry field untouched, so a device is never mislabeled
// with stale or hardcoded values.
func enrichDeviceClassification(
	registry *eebus.DeviceRegistry,
	localEntity spineapi.EntityLocalInterface,
	ski string,
	device spineapi.DeviceRemoteInterface,
	entity spineapi.EntityRemoteInterface,
) {
	if registry == nil {
		return
	}

	var deviceType string
	if device != nil && device.DeviceType() != nil {
		deviceType = string(*device.DeviceType())
	}

	brand, model, serial := manufacturerDetails(localEntity, device, entity)

	if brand == "" && model == "" && serial == "" && deviceType == "" {
		return
	}
	registry.UpsertDeviceClassification(ski, brand, model, serial, deviceType)
}

// manufacturerDetails extracts brand, model and serial from the remote entity's
// DeviceClassification server feature. The manufacturer data usually lives on the
// device's main entity, so the event entity is tried first and then every entity
// of the device until a brand name is found.
func manufacturerDetails(
	localEntity spineapi.EntityLocalInterface,
	device spineapi.DeviceRemoteInterface,
	entity spineapi.EntityRemoteInterface,
) (brand, model, serial string) {
	if localEntity == nil {
		return "", "", ""
	}

	candidates := make([]spineapi.EntityRemoteInterface, 0, 4)
	if entity != nil {
		candidates = append(candidates, entity)
	}
	if device != nil {
		for _, e := range device.Entities() {
			if e != nil && e != entity {
				candidates = append(candidates, e)
			}
		}
	}

	for _, candidate := range candidates {
		dc, err := client.NewDeviceClassification(localEntity, candidate)
		if err != nil {
			continue
		}
		details, err := dc.GetManufacturerDetails()
		if err != nil || details == nil {
			continue
		}

		if brand == "" && details.BrandName != nil {
			brand = string(*details.BrandName)
		}
		if model == "" {
			if details.DeviceCode != nil && *details.DeviceCode != "" {
				model = string(*details.DeviceCode)
			} else if details.DeviceName != nil {
				model = string(*details.DeviceName)
			}
		}
		if serial == "" && details.SerialNumber != nil {
			serial = string(*details.SerialNumber)
		}

		if brand != "" && model != "" && serial != "" {
			break
		}
	}
	return brand, model, serial
}
