package usecases

import (
	eebusapi "github.com/enbility/eebus-go/api"
	spineapi "github.com/enbility/spine-go/api"

	"github.com/volschin/eebus-bridge/internal/eebus"
)

// compatibleEntity resolves the remote entity for a device SKI from a use
// case's negotiated scenario list instead of the flat device registry:
// multi-entity gateways expose several entities per device and only the
// scenario list is use-case aware (issue #47). An empty ski resolves only when
// exactly one distinct normalized device SKI is compatible. SKIs are compared
// normalized so formatting differences do not create false mismatches or
// ambiguity.
func compatibleEntity(scenarios []eebusapi.RemoteEntityScenarios, ski string) eebus.EntityResolution {
	want := eebus.NormalizeSKI(ski)
	if want != "" {
		for _, remote := range scenarios {
			entity := remote.Entity
			if entity == nil || entity.Device() == nil {
				continue
			}
			if eebus.NormalizeSKI(entity.Device().Ski()) == want {
				return eebus.EntityResolution{Entity: entity, DeviceCount: 1}
			}
		}
		return eebus.EntityResolution{}
	}

	entitiesBySKI := make(map[string]spineapi.EntityRemoteInterface)
	for _, remote := range scenarios {
		entity := remote.Entity
		if entity == nil || entity.Device() == nil {
			continue
		}
		normalizedSKI := eebus.NormalizeSKI(entity.Device().Ski())
		if _, seen := entitiesBySKI[normalizedSKI]; !seen {
			entitiesBySKI[normalizedSKI] = entity
		}
	}
	if len(entitiesBySKI) == 1 {
		for _, entity := range entitiesBySKI {
			return eebus.EntityResolution{Entity: entity, DeviceCount: 1}
		}
	}
	return eebus.EntityResolution{DeviceCount: len(entitiesBySKI)}
}
