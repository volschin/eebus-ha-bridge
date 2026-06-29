package grpc

import (
	"math"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
