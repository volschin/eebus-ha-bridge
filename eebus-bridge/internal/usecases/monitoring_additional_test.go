package usecases

import (
	"errors"
	"testing"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	mampc "github.com/enbility/eebus-go/usecases/ma/mpc"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestMonitoringBeforeSetupReturnsInitializationError(t *testing.T) {
	w := NewMonitoringWrapper(nil, nil, false)
	w.Setup(nil)

	if w.UseCase() != nil {
		t.Fatal("UseCase() before setup returned a use case")
	}
	if got := w.CompatibleEntity("abcd"); got.Entity != nil || got.Ambiguous() {
		t.Fatalf("CompatibleEntity() before setup = %+v, want no match", got)
	}

	checks := []struct {
		name string
		call func() error
	}{
		{"power", func() error { _, err := w.Power(nil); return err }},
		{"power per phase", func() error { _, err := w.PowerPerPhase(nil); return err }},
		{"energy consumed", func() error { _, err := w.EnergyConsumed(nil); return err }},
		{"energy produced", func() error { _, err := w.EnergyProduced(nil); return err }},
		{"current per phase", func() error { _, err := w.CurrentPerPhase(nil); return err }},
		{"voltage per phase", func() error { _, err := w.VoltagePerPhase(nil); return err }},
		{"frequency", func() error { _, err := w.Frequency(nil); return err }},
		{"generic measurements", func() error { _, err := w.GenericMeasurements(""); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); !errors.Is(err, errMonitoringNotInitialized) {
				t.Fatalf("error = %v, want %v", err, errMonitoringNotInitialized)
			}
		})
	}
}

func TestMonitoringRoutesRemainingEventTypes(t *testing.T) {
	tests := []struct {
		name string
		in   eebusapi.EventType
		want eebus.EventType
	}{
		{"power per phase", mampc.DataUpdatePowerPerPhase, eebus.EventTypeMonitoringPowerPerPhaseUpdated},
		{"energy produced", mampc.DataUpdateEnergyProduced, eebus.EventTypeMonitoringEnergyProducedUpdated},
		{"currents per phase", mampc.DataUpdateCurrentsPerPhase, eebus.EventTypeMonitoringCurrentsPerPhaseUpdated},
		{"voltage per phase", mampc.DataUpdateVoltagePerPhase, eebus.EventTypeMonitoringVoltagePerPhaseUpdated},
		{"support", mampc.UseCaseSupportUpdate, eebus.EventTypeMonitoringUseCaseSupportUpdated},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bus := eebus.NewEventBus()
			ch := bus.Subscribe()
			defer bus.Unsubscribe(ch)

			w := NewMonitoringWrapper(bus, nil, false)
			w.HandleEvent("ab:cd", nil, nil, test.in)

			select {
			case event := <-ch:
				if event.Type != test.want || event.SKI != "ABCD" {
					t.Fatalf("event = %+v, want type %q and normalized SKI", event, test.want)
				}
			case <-time.After(time.Second):
				t.Fatal("timeout waiting for event")
			}
		})
	}
}

func TestMonitoringHandleEventWithoutBusAndWithDebugDoesNotPanic(t *testing.T) {
	w := NewMonitoringWrapper(nil, nil, true)
	w.HandleEvent("ab:cd", nil, nil, mampc.DataUpdatePower)
}

func TestClassifyGenericMeasurement(t *testing.T) {
	ptr := func(value string) *string { return &value }
	tests := []struct {
		name       string
		entityType string
		measType   *string
		scope      *string
		want       string
	}{
		{"DHW scope", "ignored", nil, ptr(string(model.ScopeTypeTypeDhwTemperature)), "dhw_temperature"},
		{"room scope", "ignored", nil, ptr(string(model.ScopeTypeTypeRoomAirTemperature)), "room_temperature"},
		{"outside scope", "ignored", nil, ptr(string(model.ScopeTypeTypeOutsideAirTemperature)), "outdoor_temperature"},
		{"flow scope", "ignored", nil, ptr(string(model.ScopeTypeTypeFlowTemperature)), "flow_temperature"},
		{"return scope", "ignored", nil, ptr(string(model.ScopeTypeTypeReturnTemperature)), "return_temperature"},
		{"compressor component scope", "Compressor", nil, ptr(string(model.ScopeTypeTypeComponentTemperature)), "compressor_temperature"},
		{"DHW temperature fallback", "DHWCircuit", ptr(string(model.MeasurementTypeTypeTemperature)), nil, "dhw_temperature"},
		{"room temperature fallback", "HVACRoom", ptr(string(model.MeasurementTypeTypeTemperature)), nil, "room_temperature"},
		{"outdoor temperature fallback", "TemperatureSensor", ptr(string(model.MeasurementTypeTypeTemperature)), nil, "outdoor_temperature"},
		{"compressor temperature fallback", "Compressor", ptr(string(model.MeasurementTypeTypeTemperature)), nil, "compressor_temperature"},
		{"compressor power", "Compressor", ptr(string(model.MeasurementTypeTypePower)), nil, "compressor_power"},
		{"DHW energy", "DHWCircuit", ptr(string(model.MeasurementTypeTypeEnergy)), nil, "energy_consumed_dhw"},
		{"HVAC system energy", "HVACSystem", ptr(string(model.MeasurementTypeTypeEnergy)), nil, "energy_consumed_heating"},
		{"HVAC room energy", "HVACRoom", ptr(string(model.MeasurementTypeTypeEnergy)), nil, "energy_consumed_heating"},
		{"raw complete", "Custom", ptr("customType"), ptr("customScope"), "raw_custom_customType_customScope"},
		{"raw defaults", "", nil, nil, "raw_entity_unknown_unspecified"},
		{"component on other entity", "Pump", ptr(string(model.MeasurementTypeTypeTemperature)), ptr(string(model.ScopeTypeTypeComponentTemperature)), "raw_pump_temperature_componentTemperature"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			description := model.MeasurementDescriptionDataType{}
			if test.measType != nil {
				value := model.MeasurementTypeType(*test.measType)
				description.MeasurementType = &value
			}
			if test.scope != nil {
				value := model.ScopeTypeType(*test.scope)
				description.ScopeType = &value
			}
			got := classifyGenericMeasurement(eebus.EntityInfo{Type: test.entityType}, description)
			if got != test.want {
				t.Fatalf("classification = %q, want %q", got, test.want)
			}
		})
	}
}

func TestStringPtrValue(t *testing.T) {
	if got := stringPtrValue[string](nil); got != "" {
		t.Fatalf("nil value = %q, want empty", got)
	}
	value := "value"
	if got := stringPtrValue(&value); got != value {
		t.Fatalf("value = %q, want %q", got, value)
	}
}

func TestHasMeasurementServer(t *testing.T) {
	if hasMeasurementServer(nil) {
		t.Fatal("nil feature list reported Measurement/server")
	}
	if hasMeasurementServer([]string{"Measurement/client", "DeviceDiagnosis/server"}) {
		t.Fatal("unrelated feature list reported Measurement/server")
	}
	if !hasMeasurementServer([]string{"DeviceDiagnosis/server", "Measurement/server"}) {
		t.Fatal("Measurement/server was not detected")
	}
}
