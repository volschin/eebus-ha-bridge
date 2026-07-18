package grpc

import (
	"strings"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type measurementDefinition struct {
	id   pb.MeasurementId
	unit string
}

// measurementCatalog is the single transport mapping from bridge/EEBUS
// measurement metadata to the stable v1 catalog and canonical unit.
var measurementCatalog = map[string]measurementDefinition{
	"power_consumption":       {pb.MeasurementId_MEASUREMENT_ID_POWER_CONSUMPTION, "W"},
	"energy_consumed":         {pb.MeasurementId_MEASUREMENT_ID_ENERGY_CONSUMED, "kWh"},
	"power_l1":                {pb.MeasurementId_MEASUREMENT_ID_POWER_L1, "W"},
	"power_l2":                {pb.MeasurementId_MEASUREMENT_ID_POWER_L2, "W"},
	"power_l3":                {pb.MeasurementId_MEASUREMENT_ID_POWER_L3, "W"},
	"current_l1":              {pb.MeasurementId_MEASUREMENT_ID_CURRENT_L1, "A"},
	"current_l2":              {pb.MeasurementId_MEASUREMENT_ID_CURRENT_L2, "A"},
	"current_l3":              {pb.MeasurementId_MEASUREMENT_ID_CURRENT_L3, "A"},
	"voltage_l1":              {pb.MeasurementId_MEASUREMENT_ID_VOLTAGE_L1, "V"},
	"voltage_l2":              {pb.MeasurementId_MEASUREMENT_ID_VOLTAGE_L2, "V"},
	"voltage_l3":              {pb.MeasurementId_MEASUREMENT_ID_VOLTAGE_L3, "V"},
	"frequency":               {pb.MeasurementId_MEASUREMENT_ID_FREQUENCY, "Hz"},
	"energy_produced":         {pb.MeasurementId_MEASUREMENT_ID_ENERGY_PRODUCED, "kWh"},
	"dhw_temperature":         {pb.MeasurementId_MEASUREMENT_ID_DHW_TEMPERATURE, "degC"},
	"room_temperature":        {pb.MeasurementId_MEASUREMENT_ID_ROOM_TEMPERATURE, "degC"},
	"outdoor_temperature":     {pb.MeasurementId_MEASUREMENT_ID_OUTDOOR_TEMPERATURE, "degC"},
	"flow_temperature":        {pb.MeasurementId_MEASUREMENT_ID_FLOW_TEMPERATURE, "degC"},
	"return_temperature":      {pb.MeasurementId_MEASUREMENT_ID_RETURN_TEMPERATURE, "degC"},
	"compressor_temperature":  {pb.MeasurementId_MEASUREMENT_ID_COMPRESSOR_TEMPERATURE, "degC"},
	"compressor_power":        {pb.MeasurementId_MEASUREMENT_ID_COMPRESSOR_POWER, "W"},
	"energy_consumed_heating": {pb.MeasurementId_MEASUREMENT_ID_ENERGY_CONSUMED_HEATING, "kWh"},
	"energy_consumed_dhw":     {pb.MeasurementId_MEASUREMENT_ID_ENERGY_CONSUMED_DHW, "kWh"},
}

func newMeasurementEntry(measurementType string, value float64, unit string, timestamp *timestamppb.Timestamp) *pb.MeasurementEntry {
	entry := &pb.MeasurementEntry{Type: measurementType, Value: value, Unit: unit, Timestamp: timestamp}
	definition, known := measurementCatalog[strings.ToLower(strings.TrimSpace(measurementType))]
	if !known {
		return entry
	}
	normalized, compatible := normalizeMeasurementValue(value, unit, definition.unit)
	if !compatible {
		// Keep the value visible for diagnostics without letting legacy clients
		// consume it under a known type with unsafe units.
		entry.Type = "invalid_unit_" + strings.ToLower(strings.TrimSpace(measurementType))
		return entry
	}
	entry.Id = definition.id.Enum()
	entry.Value = normalized
	entry.Unit = definition.unit
	return entry
}

func normalizeMeasurementValue(value float64, actual, canonical string) (float64, bool) {
	actual = strings.TrimSpace(actual)
	if actual == canonical {
		return value, true
	}
	switch canonical {
	case "W":
		if actual == "kW" {
			return value * 1000, true
		}
	case "A":
		if actual == "mA" {
			return value / 1000, true
		}
	case "V":
		if actual == "mV" {
			return value / 1000, true
		}
	case "kWh":
		if actual == "Wh" {
			return value / 1000, true
		}
		if actual == "MWh" {
			return value * 1000, true
		}
	case "degC":
		if actual == "°C" || actual == "C" {
			return value, true
		}
	}
	return 0, false
}
