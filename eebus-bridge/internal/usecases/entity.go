package usecases

import (
	eebusapi "github.com/enbility/eebus-go/api"
	spineapi "github.com/enbility/spine-go/api"

	"github.com/volschin/eebus-bridge/internal/eebus"
)

// compatibleEntity resolves the remote entity for a device SKI from a use
// case's negotiated scenario list instead of the flat device registry:
// multi-entity gateways expose several entities per device and only the
// scenario list is use-case aware (issue #47). An empty ski matches the
// first compatible entity of any device. SKIs are compared normalized so
// case and spacing differences reported by the remote do not cause a
// spurious mismatch. Returns nil when no compatible entity has been
// negotiated yet.
func compatibleEntity(scenarios []eebusapi.RemoteEntityScenarios, ski string) spineapi.EntityRemoteInterface {
	want := eebus.NormalizeSKI(ski)
	for _, remote := range scenarios {
		entity := remote.Entity
		if entity == nil || entity.Device() == nil {
			continue
		}
		if want == "" || eebus.NormalizeSKI(entity.Device().Ski()) == want {
			return entity
		}
	}
	return nil
}
