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
		{"empty want matches any", "abcd1234", "", true},
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
			if (got != nil) != tt.match {
				t.Errorf("compatibleEntity(ski=%q, want=%q) matched=%v, want %v",
					tt.entitySKI, tt.want, got != nil, tt.match)
			}
		})
	}
}

func TestCompatibleEntitySkipsNilEntries(t *testing.T) {
	scenarios := []eebusapi.RemoteEntityScenarios{
		{Entity: nil},
		scenarioWithSKI(t, "abcd1234"),
	}
	if got := compatibleEntity(scenarios, "abcd1234"); got == nil {
		t.Error("compatibleEntity skipped past nil entry but did not find match")
	}
}

// CompatibleEntity must not panic and must return nil before Setup (uc == nil),
// so resolveEntity falls back to the registry instead of crashing.
func TestCompatibleEntityNilUseCase(t *testing.T) {
	w := NewLPCWrapper(nil, nil, false)
	if got := w.CompatibleEntity("abcd1234"); got != nil {
		t.Errorf("CompatibleEntity on un-set-up wrapper = %v, want nil", got)
	}
}
