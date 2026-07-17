package grpc

import (
	"context"
	"math"
	"testing"
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
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

func TestValidSKI(t *testing.T) {
	valid := "682f708ceba5df9adcb9e6787ea911d9fc3ac490"
	if !validSKI(valid) {
		t.Errorf("validSKI(%q) = false, want true", valid)
	}
	if !validSKI("682F708CEBA5DF9ADCB9E6787EA911D9FC3AC490") {
		t.Error("validSKI should accept uppercase hex")
	}

	invalid := []string{
		"",
		"too-short",
		valid + "a", // 41 chars
		"682f708ceba5df9adcb9e6787ea911d9fc3ac49z",     // non-hex trailing char
		"68:2f:70:8c:eb:a5:df:9a:dc:b9:e6:78:7e:a9:11", // colon-separated, wrong length
	}
	for _, ski := range invalid {
		if validSKI(ski) {
			t.Errorf("validSKI(%q) = true, want false", ski)
		}
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

	if _, err := grid.PublishGridData(ctx, &pb.GridData{PowerW: ptrFloat64(inf)}); true {
		assertInvalid(t, err)
	}
	if _, err := grid.PublishGridData(ctx, &pb.GridData{PowerW: ptrFloat64(100), FeedInWh: &neg}); true {
		assertInvalid(t, err)
	}
	if _, err := viz.PublishPVData(ctx, &pb.PVData{PowerW: ptrFloat64(neg)}); true {
		assertInvalid(t, err)
	}
	if _, err := viz.PublishBatteryData(ctx, &pb.BatteryData{PowerW: ptrFloat64(0), StateOfChargePct: &soc}); true {
		assertInvalid(t, err)
	}
}

func TestProviderValidityAllowsSmallHostClockSkew(t *testing.T) {
	now := time.Now()
	_, err := providerValidity(&pb.ProviderSampleMeta{
		ObservedAt: timestamppb.New(now.Add(time.Second)),
		ValidUntil: timestamppb.New(now.Add(time.Minute)),
	})
	if err != nil {
		t.Fatalf("providerValidity rejected small future observed_at: %v", err)
	}

	_, err = providerValidity(&pb.ProviderSampleMeta{
		ObservedAt: timestamppb.New(now.Add(time.Minute)),
		ValidUntil: timestamppb.New(now.Add(2 * time.Minute)),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("providerValidity far future code = %v, want InvalidArgument", status.Code(err))
	}
}
