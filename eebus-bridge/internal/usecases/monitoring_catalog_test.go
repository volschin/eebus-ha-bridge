package usecases

import (
	"testing"

	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestGenericEnergyClassificationUsesEntityMetadata(t *testing.T) {
	energy := model.MeasurementTypeTypeEnergy
	description := model.MeasurementDescriptionDataType{MeasurementType: &energy}
	tests := []struct {
		entityType string
		want       string
	}{
		{"HVACSystem", "energy_consumed_heating"},
		{"HVACRoom", "energy_consumed_heating"},
		{"DHWCircuit", "energy_consumed_dhw"},
	}
	for _, test := range tests {
		t.Run(test.entityType, func(t *testing.T) {
			if got := classifyGenericMeasurement(eebus.EntityInfo{Type: test.entityType}, description); got != test.want {
				t.Fatalf("classification = %q, want %q", got, test.want)
			}
		})
	}
}

