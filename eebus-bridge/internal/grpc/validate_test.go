package grpc

import (
	"context"
	"math"
	"testing"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFiniteNonNegativePercent(t *testing.T) {
	inf := math.Inf(1)
	nan := math.NaN()

	if finite("x", 0) != nil || finite("x", -5) != nil {
		t.Error("finite should accept any finite value, including negatives")
	}
	if finite("x", nan) == nil || finite("x", inf) == nil {
		t.Error("finite should reject NaN/Inf")
	}
	if nonNegative("x", 0) != nil || nonNegative("x", 5) != nil {
		t.Error("nonNegative should accept >= 0")
	}
	if nonNegative("x", -0.1) == nil || nonNegative("x", nan) == nil {
		t.Error("nonNegative should reject negatives and NaN")
	}
	if percent("x", 0) != nil || percent("x", 100) != nil || percent("x", 42) != nil {
		t.Error("percent should accept 0..100")
	}
	if percent("x", -1) == nil || percent("x", 100.1) == nil || percent("x", inf) == nil {
		t.Error("percent should reject out-of-range and non-finite")
	}
}

// Validation runs before the provider-nil check, so a nil provider still yields
// InvalidArgument for bad input (rather than Unavailable).
func TestPublishRPCsRejectInvalidValues(t *testing.T) {
	inf := math.Inf(1)
	neg := -1.0
	soc := 250.0

	grid := NewGridService(nil)
	viz := NewVisualizationService(nil, nil)
	ctx := context.Background()

	assertInvalid := func(t *testing.T, err error) {
		t.Helper()
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("want InvalidArgument, got %v", err)
		}
	}

	if _, err := grid.PublishGridData(ctx, &pb.GridData{PowerW: inf}); true {
		assertInvalid(t, err)
	}
	if _, err := grid.PublishGridData(ctx, &pb.GridData{PowerW: 100, FeedInWh: &neg}); true {
		assertInvalid(t, err)
	}
	if _, err := viz.PublishPVData(ctx, &pb.PVData{PowerW: neg}); true {
		assertInvalid(t, err)
	}
	if _, err := viz.PublishBatteryData(ctx, &pb.BatteryData{PowerW: 0, StateOfChargePct: &soc}); true {
		assertInvalid(t, err)
	}
}
