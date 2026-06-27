package eebus

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

// UseCaseDiscovery logs the full advertised EEBUS use-case map of a remote device
// exactly once per SKI. It is a diagnostic aid for deciding which provider-side
// use cases a given gateway actually speaks before building them — e.g. whether a
// Vaillant VR940f exposes a path for feeding grid/PV data to the heat pump (MGCP /
// VAPD). See docs/eebus-vaillant-improvements.md, "Recommended next steps" step 1.
//
// The dump lists, per remote entity, its type, address and server/client features
// (Measurement, ElectricalConnection, DeviceConfiguration are the ones a provider
// side needs), plus every advertised use case with actor, version, availability and
// supported scenarios.
type UseCaseDiscovery struct {
	mu     sync.Mutex
	logged map[string]bool
	logf   func(format string, args ...any)
}

// NewUseCaseDiscovery returns a discovery logger that writes through logf. Passing
// nil uses log.Printf. Construct a fresh instance in tests to exercise dedup.
func NewUseCaseDiscovery(logf func(format string, args ...any)) *UseCaseDiscovery {
	if logf == nil {
		logf = log.Printf
	}
	return &UseCaseDiscovery{logged: make(map[string]bool), logf: logf}
}

var defaultUseCaseDiscovery = NewUseCaseDiscovery(nil)

// DefaultUseCaseDiscovery returns the process-wide discovery logger shared by the
// use-case wrappers, so a device is dumped once regardless of which use-case
// callback observes it first.
func DefaultUseCaseDiscovery() *UseCaseDiscovery { return defaultUseCaseDiscovery }

// LogOnce dumps the device's advertised use-case map the first time it sees a SKI.
// Subsequent calls for the same (normalized) SKI are no-ops. Safe for nil device.
func (d *UseCaseDiscovery) LogOnce(ski string, device spineapi.DeviceRemoteInterface) {
	if d == nil || device == nil {
		return
	}
	ski = NormalizeSKI(ski)
	if ski == "" {
		ski = NormalizeSKI(device.Ski())
	}

	d.mu.Lock()
	if d.logged[ski] {
		d.mu.Unlock()
		return
	}
	d.logged[ski] = true
	d.mu.Unlock()

	d.logf("%s", FormatDeviceUseCases(ski, device))
}

// FormatDeviceUseCases renders a human-readable, greppable ("[DISCOVERY]") dump of
// a remote device's entities, features and advertised use cases. Pure function: no
// side effects, safe for unit testing against eebus-go remote-device fakes.
func FormatDeviceUseCases(ski string, device spineapi.DeviceRemoteInterface) string {
	var b strings.Builder

	deviceType := ""
	if dt := device.DeviceType(); dt != nil {
		deviceType = string(*dt)
	}
	entities := device.Entities()
	fmt.Fprintf(&b, "[DISCOVERY] device ski=%s type=%s entities=%d\n", ski, deviceType, len(entities))

	for _, e := range entities {
		if e == nil {
			continue
		}
		fmt.Fprintf(&b, "[DISCOVERY]   entity addr=%s type=%s features=[%s]\n",
			formatEntityAddress(e.Address()), e.EntityType(), formatFeatures(e.Features()))
	}

	useCases := device.UseCases()
	if len(useCases) == 0 {
		b.WriteString("[DISCOVERY]   (no use cases advertised yet)\n")
		return strings.TrimRight(b.String(), "\n")
	}

	for _, uc := range useCases {
		actor := ""
		if uc.Actor != nil {
			actor = string(*uc.Actor)
		}
		for _, sup := range uc.UseCaseSupport {
			name := ""
			if sup.UseCaseName != nil {
				name = string(*sup.UseCaseName)
			}
			version := ""
			if sup.UseCaseVersion != nil {
				version = string(*sup.UseCaseVersion)
			}
			available := false
			if sup.UseCaseAvailable != nil {
				available = *sup.UseCaseAvailable
			}
			scenarios := make([]string, 0, len(sup.ScenarioSupport))
			for _, sc := range sup.ScenarioSupport {
				scenarios = append(scenarios, fmt.Sprintf("%d", uint(sc)))
			}
			fmt.Fprintf(&b, "[DISCOVERY]   usecase actor=%s name=%s v%s available=%t scenarios=[%s]\n",
				actor, name, version, available, strings.Join(scenarios, ","))
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

// formatEntityAddress renders the entity address path (e.g. "1" or "1:2") for the
// dump. Returns "?" when the address is absent.
func formatEntityAddress(addr *model.EntityAddressType) string {
	if addr == nil || len(addr.Entity) == 0 {
		return "?"
	}
	parts := make([]string, 0, len(addr.Entity))
	for _, a := range addr.Entity {
		parts = append(parts, fmt.Sprintf("%d", uint(a)))
	}
	return strings.Join(parts, ":")
}

// formatFeatures renders a sorted, de-duplicated "Type/Role" list of a remote
// entity's features. Sorting keeps the dump stable across runs for diffing.
func formatFeatures(features []spineapi.FeatureRemoteInterface) string {
	seen := make(map[string]struct{}, len(features))
	out := make([]string, 0, len(features))
	for _, f := range features {
		if f == nil {
			continue
		}
		key := fmt.Sprintf("%s/%s", f.Type(), f.Role())
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
