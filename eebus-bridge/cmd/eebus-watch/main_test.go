package main

import (
	"testing"
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestSelectTargetSKI(t *testing.T) {
	t.Parallel()

	devices := []*pb.PairedDevice{
		{},
		{Ski: "  "},
		{Ski: "123456"},
	}

	if got := selectTargetSKI("", devices); got != "123456" {
		t.Fatalf("selectTargetSKI() = %q, want %q", got, "123456")
	}
	if got := selectTargetSKI(" explicit ", devices); got != "explicit" {
		t.Fatalf("selectTargetSKI() = %q, want %q", got, "explicit")
	}
	if got := selectTargetSKI("", nil); got != "" {
		t.Fatalf("selectTargetSKI() = %q, want empty string", got)
	}
}

func TestSortedMeasurements(t *testing.T) {
	t.Parallel()

	rows := sortedMeasurements([]*pb.MeasurementEntry{
		{Type: "b", Value: 2, Unit: "W", Timestamp: timestamppb.New(unixTime(t, 2))},
		{Type: "a", Value: 1, Unit: "W", Timestamp: timestamppb.New(unixTime(t, 1))},
		{Type: "a", Value: 3, Unit: "W", Timestamp: timestamppb.New(unixTime(t, 3))},
		nil,
	})

	if len(rows) != 3 {
		t.Fatalf("sortedMeasurements() len = %d, want 3", len(rows))
	}
	if rows[0].Type != "a" || rows[0].Timestamp != "1970-01-01T00:00:01Z" {
		t.Fatalf("sortedMeasurements()[0] = %+v, want type a at 1970-01-01T00:00:01Z", rows[0])
	}
	if rows[1].Type != "a" || rows[1].Timestamp != "1970-01-01T00:00:03Z" {
		t.Fatalf("sortedMeasurements()[1] = %+v, want type a at 1970-01-01T00:00:03Z", rows[1])
	}
	if rows[2].Type != "b" {
		t.Fatalf("sortedMeasurements()[2] = %+v, want type b", rows[2])
	}
}

func TestIgnorableErr(t *testing.T) {
	t.Parallel()

	for _, code := range []codes.Code{codes.NotFound, codes.Unavailable, codes.Unimplemented} {
		if !isIgnorableErr(status.Error(code, "test")) {
			t.Fatalf("isIgnorableErr(%v) = false, want true", code)
		}
	}
	if isIgnorableErr(status.Error(codes.Internal, "test")) {
		t.Fatal("isIgnorableErr(Internal) = true, want false")
	}
}

func TestFormatTimestamp(t *testing.T) {
	t.Parallel()

	if got := formatTimestamp(nil); got != "-" {
		t.Fatalf("formatTimestamp(nil) = %q, want -", got)
	}
	if got := formatTimestamp(timestamppb.New(unixTime(t, 5))); got != "1970-01-01T00:00:05Z" {
		t.Fatalf("formatTimestamp() = %q, want 1970-01-01T00:00:05Z", got)
	}
}

func unixTime(t *testing.T, seconds int64) time.Time {
	t.Helper()
	return time.Unix(seconds, 0).UTC()
}
