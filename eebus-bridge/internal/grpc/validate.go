package grpc

import (
	"math"
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const legacyProviderSampleTTL = 2 * time.Minute

// validSKI reports whether ski is a well-formed EEBUS SKI: the SHA-1
// fingerprint of a device certificate, 40 hex characters. Callers must
// normalize with eebus.NormalizeSKI first. Accepting malformed input here
// would forward it straight to eebus-go's SHIP trust registration, which
// fails silently downstream instead of with a clear rejection at the RPC
// boundary.
func validSKI(ski string) bool {
	if len(ski) != 40 {
		return false
	}
	for _, r := range ski {
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

// Value validation for the provider push RPCs. Invalid inputs (NaN/Inf, negative
// counters, out-of-range SoC) would otherwise be serialized into spine-go scaled
// numbers and advertised to downstream equipment as real readings, producing
// silent bad optimisation/display data that is hard to diagnose. Handlers reject
// them with InvalidArgument before the value reaches a provider.

// finite rejects NaN and ±Inf. Used for signed quantities (grid/battery power)
// where any finite value is physically meaningful.
func finite(name string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return status.Errorf(codes.InvalidArgument, "%s must be a finite number", name)
	}
	return nil
}

// nonNegative additionally enforces v >= 0, for cumulative energy counters and
// quantities (e.g. PV power) that have no physical negative value.
func nonNegative(name string, v float64) error {
	if err := finite(name, v); err != nil {
		return err
	}
	if v < 0 {
		return status.Errorf(codes.InvalidArgument, "%s must not be negative", name)
	}
	return nil
}

// percent enforces a finite 0..100 value, for battery state of charge.
func percent(name string, v float64) error {
	if err := finite(name, v); err != nil {
		return err
	}
	if v < 0 || v > 100 {
		return status.Errorf(codes.InvalidArgument, "%s must be between 0 and 100, got %g", name, v)
	}
	return nil
}

func providerValidity(meta *pb.ProviderSampleMeta) (usecases.ProviderValidity, error) {
	if meta == nil {
		observedAt := time.Now()
		return usecases.ProviderValidity{
			ObservedAt: observedAt,
			ValidUntil: observedAt.Add(legacyProviderSampleTTL),
		}, nil
	}
	if meta.ObservedAt == nil {
		return usecases.ProviderValidity{}, status.Error(codes.InvalidArgument, "sample observed_at is required")
	}
	if meta.ValidUntil == nil {
		return usecases.ProviderValidity{}, status.Error(codes.InvalidArgument, "sample valid_until is required")
	}
	if err := meta.ObservedAt.CheckValid(); err != nil {
		return usecases.ProviderValidity{}, status.Errorf(codes.InvalidArgument, "sample observed_at is invalid: %v", err)
	}
	if err := meta.ValidUntil.CheckValid(); err != nil {
		return usecases.ProviderValidity{}, status.Errorf(codes.InvalidArgument, "sample valid_until is invalid: %v", err)
	}
	observedAt := meta.ObservedAt.AsTime()
	validUntil := meta.ValidUntil.AsTime()
	now := time.Now()
	if !meta.Invalid && observedAt.After(now) {
		return usecases.ProviderValidity{}, status.Error(codes.InvalidArgument, "sample observed_at must not be in the future")
	}
	if !meta.Invalid && !validUntil.After(observedAt) {
		return usecases.ProviderValidity{}, status.Error(codes.InvalidArgument, "sample valid_until must be after observed_at")
	}
	if !meta.Invalid && !validUntil.After(now) {
		return usecases.ProviderValidity{}, status.Error(codes.InvalidArgument, "sample is expired")
	}
	return usecases.ProviderValidity{
		ObservedAt: observedAt,
		ValidUntil: validUntil,
		Invalid:    meta.Invalid,
	}, nil
}
