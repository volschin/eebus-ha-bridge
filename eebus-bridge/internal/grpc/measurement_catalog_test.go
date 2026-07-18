package grpc

import (
	"testing"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestMeasurementCatalogHasUniqueStableIDsAndCanonicalUnits(t *testing.T) {
	if len(measurementCatalog) != 22 {
		t.Fatalf("catalog size = %d, want 22", len(measurementCatalog))
	}
	seen := make(map[pb.MeasurementId]string, len(measurementCatalog))
	for legacyType, definition := range measurementCatalog {
		if definition.id == pb.MeasurementId_MEASUREMENT_ID_UNSPECIFIED || definition.unit == "" {
			t.Fatalf("invalid definition %q = %+v", legacyType, definition)
		}
		if previous, exists := seen[definition.id]; exists {
			t.Fatalf("measurement ID %s shared by %q and %q", definition.id, previous, legacyType)
		}
		seen[definition.id] = legacyType
		entry := newMeasurementEntry(legacyType, 0, definition.unit, timestamppb.Now())
		if entry.Id == nil || entry.GetId() != definition.id || entry.GetUnit() != definition.unit || entry.GetValue() != 0 {
			t.Fatalf("entry for %q = %+v", legacyType, entry)
		}
	}
}

func TestMeasurementCatalogAndSnapshotFieldsCoverSameIDs(t *testing.T) {
	catalogIDs := make(map[pb.MeasurementId]struct{}, len(measurementCatalog))
	for _, definition := range measurementCatalog {
		catalogIDs[definition.id] = struct{}{}
	}
	if len(catalogIDs) != len(snapshotMeasurementFields) {
		t.Fatalf("measurement ID counts differ: catalog=%d snapshot=%d", len(catalogIDs), len(snapshotMeasurementFields))
	}
	for id := range catalogIDs {
		if _, ok := snapshotMeasurementFields[id]; !ok {
			t.Errorf("measurement ID %s is missing from snapshot fields", id)
		}
	}
	for id := range snapshotMeasurementFields {
		if _, ok := catalogIDs[id]; !ok {
			t.Errorf("snapshot measurement ID %s is missing from measurement catalog", id)
		}
	}
}

func TestMeasurementCatalogNormalizesUnitsWithoutRelabelingInvalidValues(t *testing.T) {
	energy := newMeasurementEntry("energy_consumed", 1500, "Wh", timestamppb.Now())
	if energy.GetId() != pb.MeasurementId_MEASUREMENT_ID_ENERGY_CONSUMED || energy.GetValue() != 1.5 || energy.GetUnit() != "kWh" {
		t.Fatalf("normalized energy = %+v", energy)
	}
	invalid := newMeasurementEntry("energy_consumed", 1, "joule", timestamppb.Now())
	if invalid.Id != nil || invalid.GetUnit() != "joule" || invalid.GetType() != "invalid_unit_energy_consumed" {
		t.Fatalf("invalid unit was falsely catalogued: %+v", invalid)
	}
}
