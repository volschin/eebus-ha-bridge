package usecases

import (
	"errors"
	"log"
	"sync"

	"github.com/enbility/eebus-go/features/client"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

type deviceClassificationDetails struct {
	brand            string
	model            string
	serial           string
	softwareRevision string
	hardwareRevision string
}

// DeviceClassifier owns the generic DeviceClassification client feature. It
// requests manufacturer data as soon as a remote entity is discovered and
// persists later responses independently of any HVAC or monitoring use case.
type DeviceClassifier struct {
	localEntity spineapi.EntityLocalInterface
	registry    *eebus.DeviceRegistry
	bus         *eebus.EventBus

	// requested tracks devices for which an active manufacturer-data read has
	// already been issued, so we request (and, on rejection, log) at most once
	// per device instead of once per classification-server entity on every
	// reconnect. Some gateways (e.g. Vaillant VR940) reject the read with
	// "operation is not supported on function deviceClassificationManufacturerData";
	// without this guard that rejection is logged for every entity on every
	// SHIP reconnect.
	mu        sync.Mutex
	requested map[string]bool
}

func NewDeviceClassifier(registry *eebus.DeviceRegistry, bus *eebus.EventBus) *DeviceClassifier {
	return &DeviceClassifier{registry: registry, bus: bus, requested: make(map[string]bool)}
}

func (c *DeviceClassifier) Setup(localEntity spineapi.EntityLocalInterface) error {
	if localEntity == nil {
		return errors.New("device classifier local entity is required")
	}
	if localEntity.GetOrAddFeature(model.FeatureTypeTypeDeviceClassification, model.RoleTypeClient) == nil {
		return errors.New("failed to add DeviceClassification client feature")
	}
	c.localEntity = localEntity
	if err := localEntity.Device().Events().Subscribe(c); err != nil {
		return errors.New("subscribing device classifier: " + err.Error())
	}
	return nil
}

func (c *DeviceClassifier) HandleEvent(payload spineapi.EventPayload) {
	if c == nil || c.localEntity == nil || c.registry == nil {
		return
	}
	device := payload.Device
	if device == nil && payload.Entity != nil {
		device = payload.Entity.Device()
	}

	if data, ok := payload.Data.(*model.DeviceClassificationManufacturerDataType); ok {
		c.store(payload.Ski, device, classificationDetails(data))
		return
	}
	if payload.ChangeType != spineapi.ElementChangeAdd {
		return
	}
	if payload.Entity != nil {
		c.readOrRequest(payload.Ski, device, payload.Entity)
		return
	}
	if device != nil {
		for _, entity := range device.Entities() {
			c.readOrRequest(payload.Ski, device, entity)
		}
	}
}

func (c *DeviceClassifier) readOrRequest(
	ski string,
	device spineapi.DeviceRemoteInterface,
	entity spineapi.EntityRemoteInterface,
) {
	if entity == nil || entity.FeatureOfTypeAndRole(
		model.FeatureTypeTypeDeviceClassification,
		model.RoleTypeServer,
	) == nil {
		return
	}
	dc, err := client.NewDeviceClassification(c.localEntity, entity)
	if err != nil {
		return
	}
	if data, readErr := dc.GetManufacturerDetails(); readErr == nil && data != nil {
		c.store(ski, device, classificationDetails(data))
		return
	}
	key := observationSKI(ski, device)
	c.mu.Lock()
	already := c.requested[key]
	c.requested[key] = true
	c.mu.Unlock()
	if already {
		return
	}
	if _, err := dc.RequestManufacturerDetails(); err != nil {
		log.Printf(
			"requesting DeviceClassification data for %s: %v",
			eebus.ShortSKI(key),
			err,
		)
	}
}

func (c *DeviceClassifier) store(
	ski string,
	device spineapi.DeviceRemoteInterface,
	details deviceClassificationDetails,
) {
	ski = observationSKI(ski, device)
	var deviceType string
	if device != nil && device.DeviceType() != nil {
		deviceType = string(*device.DeviceType())
	}
	// Ignore payloads that carry no usable field (every element of
	// DeviceClassificationManufacturerDataType is optional). Without this guard
	// an all-nil response can still flip the registry's SKI-only record to
	// changed=true and fan out a spurious classification resync. Mirrors the
	// guard in enrichDeviceClassification.
	if details == (deviceClassificationDetails{}) && deviceType == "" {
		return
	}
	changed := c.registry.UpsertDeviceClassification(
		ski,
		details.brand,
		details.model,
		details.serial,
		deviceType,
		details.softwareRevision,
		details.hardwareRevision,
	)
	if changed && c.bus != nil {
		c.bus.Publish(eebus.Event{SKI: ski, Type: eebus.EventTypeDeviceClassificationUpdated})
	}
}

// enrichDeviceClassification is a cache-only fallback for existing use-case
// callbacks. DeviceClassifier owns the request and update lifecycle.
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
	details := manufacturerDetails(localEntity, device, entity)
	if details == (deviceClassificationDetails{}) && deviceType == "" {
		return
	}
	registry.UpsertDeviceClassification(
		ski,
		details.brand,
		details.model,
		details.serial,
		deviceType,
		details.softwareRevision,
		details.hardwareRevision,
	)
}

// manufacturerDetails extracts manufacturer data from every classification
// server feature cached for the remote device. Partial values from different
// entities are merged without replacing already found fields.
func manufacturerDetails(
	localEntity spineapi.EntityLocalInterface,
	device spineapi.DeviceRemoteInterface,
	entity spineapi.EntityRemoteInterface,
) deviceClassificationDetails {
	if localEntity == nil {
		return deviceClassificationDetails{}
	}

	candidates := make([]spineapi.EntityRemoteInterface, 0, 4)
	if entity != nil {
		candidates = append(candidates, entity)
	}
	if device != nil {
		for _, candidate := range device.Entities() {
			if candidate != nil && candidate != entity {
				candidates = append(candidates, candidate)
			}
		}
	}

	var result deviceClassificationDetails
	for _, candidate := range candidates {
		dc, err := client.NewDeviceClassification(localEntity, candidate)
		if err != nil {
			continue
		}
		data, err := dc.GetManufacturerDetails()
		if err != nil || data == nil {
			continue
		}
		mergeClassificationDetails(&result, classificationDetails(data))
		if result.brand != "" && result.model != "" && result.serial != "" &&
			result.softwareRevision != "" && result.hardwareRevision != "" {
			break
		}
	}
	return result
}

func classificationDetails(data *model.DeviceClassificationManufacturerDataType) deviceClassificationDetails {
	if data == nil {
		return deviceClassificationDetails{}
	}
	result := deviceClassificationDetails{
		brand:            classificationString(data.BrandName),
		serial:           classificationString(data.SerialNumber),
		softwareRevision: classificationString(data.SoftwareRevision),
		hardwareRevision: classificationString(data.HardwareRevision),
	}
	if result.brand == "" {
		result.brand = classificationString(data.VendorName)
	}
	if result.brand == "" {
		result.brand = classificationString(data.VendorCode)
	}
	result.model = classificationString(data.DeviceCode)
	if result.model == "" {
		result.model = classificationString(data.DeviceName)
	}
	return result
}

func mergeClassificationDetails(target *deviceClassificationDetails, source deviceClassificationDetails) {
	if target.brand == "" {
		target.brand = source.brand
	}
	if target.model == "" {
		target.model = source.model
	}
	if target.serial == "" {
		target.serial = source.serial
	}
	if target.softwareRevision == "" {
		target.softwareRevision = source.softwareRevision
	}
	if target.hardwareRevision == "" {
		target.hardwareRevision = source.hardwareRevision
	}
}

func classificationString(value *model.DeviceClassificationStringType) string {
	if value == nil {
		return ""
	}
	return string(*value)
}
