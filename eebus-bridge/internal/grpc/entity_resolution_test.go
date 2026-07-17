package grpc

import (
	"errors"
	"testing"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestResolveCompatibleEntityContract(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	entityA := mocks.NewEntityRemoteInterface(t)
	entityB := mocks.NewEntityRemoteInterface(t)
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{
		RemoteEntities: []spineapi.EntityRemoteInterface{entityA},
	})
	registry.AddDevice(testOtherValidSKI, eebus.DeviceInfo{
		RemoteEntities: []spineapi.EntityRemoteInterface{entityB},
	})
	resolver := func(ski string) eebus.EntityResolution {
		switch eebus.NormalizeSKI(ski) {
		case "":
			return eebus.EntityResolution{DeviceCount: 2}
		case eebus.NormalizeSKI(testValidSKI):
			return eebus.EntityResolution{Entity: entityA, DeviceCount: 1}
		case eebus.NormalizeSKI(testOtherValidSKI):
			return eebus.EntityResolution{Entity: entityB, DeviceCount: 1}
		default:
			return eebus.EntityResolution{}
		}
	}

	if _, err := resolveCompatibleEntity("", "LPC entity", eebus.CapabilityLPC, registry, resolver); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("empty SKI error = %v, want FailedPrecondition", err)
	}
	singleDeviceResolver := func(ski string) eebus.EntityResolution {
		if eebus.NormalizeSKI(ski) == "" {
			return eebus.EntityResolution{Entity: entityA, DeviceCount: 1}
		}
		return resolver(ski)
	}
	got, err := resolveCompatibleEntity("", "LPC entity", eebus.CapabilityLPC, registry, singleDeviceResolver)
	if err != nil {
		t.Fatalf("single-device empty SKI resolve: %v", err)
	}
	if got != entityA {
		t.Fatalf("single-device empty SKI selected wrong entity")
	}
	for _, test := range []struct {
		ski  string
		want spineapi.EntityRemoteInterface
	}{
		{ski: testValidSKI, want: entityA},
		{ski: testOtherValidSKI, want: entityB},
	} {
		got, err := resolveCompatibleEntity(test.ski, "LPC entity", eebus.CapabilityLPC, registry, resolver)
		if err != nil {
			t.Fatalf("resolveEntity(%q): %v", test.ski, err)
		}
		if got != test.want {
			t.Errorf("resolveEntity(%q) selected the wrong device", test.ski)
		}
	}
	if _, err := resolveCompatibleEntity(testUnknownValidSKI, "LPC entity", eebus.CapabilityLPC, registry, resolver); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown explicit SKI error = %v, want NotFound", err)
	}
	if _, err := resolveCompatibleEntity("not-a-ski", "LPC entity", eebus.CapabilityLPC, registry, resolver); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("malformed explicit SKI error = %v, want InvalidArgument", err)
	}
}

func TestReadMetricDoesNotFallbackAfterUnknownExplicitSKI(t *testing.T) {
	wantErr := status.Error(codes.NotFound, "unknown device")
	called := false
	_, err := readMetric("power", resolvedEntity{ski: "UNKNOWN", err: wantErr}, func(spineapi.EntityRemoteInterface) (float64, error) {
		called = true
		return 123, nil
	})

	if called {
		t.Error("read function was called after explicit SKI resolution failed")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("readMetric error = %v, want original NotFound", err)
	}
}
