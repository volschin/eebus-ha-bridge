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

func TestRegistryFallbackRejectsAmbiguousEmptySKI(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	entityA := mocks.NewEntityRemoteInterface(t)
	entityB := mocks.NewEntityRemoteInterface(t)
	registry.AddDevice("aa:bb-cc", eebus.DeviceInfo{
		RemoteEntities: []spineapi.EntityRemoteInterface{entityA},
	})
	registry.AddDevice("dd:ee-ff", eebus.DeviceInfo{
		RemoteEntities: []spineapi.EntityRemoteInterface{entityB},
	})
	service := NewLPCService(nil, eebus.NewEventBus(), registry)

	if _, err := service.resolveEntity(""); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("empty SKI error = %v, want FailedPrecondition", err)
	}
	for _, test := range []struct {
		ski  string
		want spineapi.EntityRemoteInterface
	}{
		{ski: " AA-BB-CC ", want: entityA},
		{ski: "DDEEFF", want: entityB},
	} {
		got, err := service.resolveEntity(test.ski)
		if err != nil {
			t.Fatalf("resolveEntity(%q): %v", test.ski, err)
		}
		if got != test.want {
			t.Errorf("resolveEntity(%q) selected the wrong device", test.ski)
		}
	}
	if _, err := service.resolveEntity("unknown"); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown explicit SKI error = %v, want NotFound", err)
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
