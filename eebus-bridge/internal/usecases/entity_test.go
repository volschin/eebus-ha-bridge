package usecases

import (
	"testing"

	eebusapi "github.com/enbility/eebus-go/api"
	"github.com/enbility/spine-go/mocks"
)

func scenarioWithSKI(t *testing.T, ski string) eebusapi.RemoteEntityScenarios {
	t.Helper()
	device := mocks.NewDeviceRemoteInterface(t)
	device.On("Ski").Return(ski).Maybe()
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("Device").Return(device).Maybe()
	return eebusapi.RemoteEntityScenarios{Entity: entity}
}

func TestCompatibleEntitySKIMatching(t *testing.T) {
	tests := []struct {
		name      string
		entitySKI string
		want      string
		match     bool
	}{
		{"empty want matches one device", "abcd1234", "", true},
		{"exact match", "abcd1234", "abcd1234", true},
		{"case insensitive", "ABCD1234", "abcd1234", true},
		{"whitespace insensitive", "ab cd 12 34", "abcd1234", true},
		{"surrounding whitespace", "  abcd1234  ", "abcd1234", true},
		{"mismatch", "abcd1234", "ffff0000", false},
		{"empty entity ski non-empty want", "", "abcd1234", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scenarios := []eebusapi.RemoteEntityScenarios{scenarioWithSKI(t, tt.entitySKI)}
			got := compatibleEntity(scenarios, tt.want)
			if (got.Entity != nil) != tt.match {
				t.Errorf("compatibleEntity(ski=%q, want=%q) matched=%v, want %v",
					tt.entitySKI, tt.want, got.Entity != nil, tt.match)
			}
		})
	}
}

func TestCompatibleEntitySkipsNilEntries(t *testing.T) {
	scenarios := []eebusapi.RemoteEntityScenarios{
		{Entity: nil},
		scenarioWithSKI(t, "abcd1234"),
	}
	if got := compatibleEntity(scenarios, "abcd1234"); got.Entity == nil {
		t.Error("compatibleEntity skipped past nil entry but did not find match")
	}
}

func TestCompatibleEntityEmptySKIDetectsAmbiguity(t *testing.T) {
	deviceA := scenarioWithSKI(t, "aa:bb-cc")
	deviceB := scenarioWithSKI(t, "DDEEFF")
	scenarios := []eebusapi.RemoteEntityScenarios{deviceA, deviceB}

	resolution := compatibleEntity(scenarios, "")
	if !resolution.Ambiguous() || resolution.DeviceCount != 2 {
		t.Fatalf("compatibleEntity(empty) = %+v, want ambiguity across 2 devices", resolution)
	}
	if got := compatibleEntity(scenarios, " AA BB CC "); got.Entity != deviceA.Entity {
		t.Error("explicit device A SKI did not resolve device A entity")
	}
	if got := compatibleEntity(scenarios, "dd:ee:ff"); got.Entity != deviceB.Entity {
		t.Error("explicit device B SKI did not resolve device B entity")
	}
	if got := compatibleEntity(scenarios, "unknown"); got.Entity != nil || got.Ambiguous() {
		t.Errorf("unknown explicit SKI = %+v, want unambiguous no-match", got)
	}
}

func TestCompatibleEntityCountsNormalizedSKIOnce(t *testing.T) {
	first := scenarioWithSKI(t, "ab:cd-ef")
	second := scenarioWithSKI(t, " AB-CD-EF ")
	resolution := compatibleEntity([]eebusapi.RemoteEntityScenarios{first, second}, "")

	if resolution.Ambiguous() || resolution.DeviceCount != 1 || resolution.Entity != first.Entity {
		t.Errorf("compatibleEntity(format variants) = %+v, want first entity for one normalized device", resolution)
	}
}

// CompatibleEntity must not panic and must return no entity before Setup (uc == nil),
// so resolveEntity falls back to the registry instead of crashing.
func TestCompatibleEntityNilUseCase(t *testing.T) {
	w := NewLPCWrapper(nil, nil, false)
	if got := w.CompatibleEntity("abcd1234"); got.Entity != nil || got.Ambiguous() {
		t.Errorf("CompatibleEntity on un-set-up wrapper = %+v, want no match", got)
	}
}
