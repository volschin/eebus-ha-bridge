package usecases

import (
	"testing"
	"time"

	shipcert "github.com/enbility/ship-go/cert"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/config"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

// TestProviderAvailabilityAnnouncesOnlyChanges pins the deduplication: a provider
// publishing every few seconds must not emit a notification per sample.
func TestProviderAvailabilityAnnouncesOnlyChanges(t *testing.T) {
	var announced []bool
	var availability providerAvailability
	availability.bind(func(available bool) { announced = append(announced, available) })

	availability.set(false)
	availability.set(false)
	availability.set(true)
	availability.set(true)
	availability.set(false)

	want := []bool{false, true, false}
	if len(announced) != len(want) {
		t.Fatalf("announced %v, want %v", announced, want)
	}
	for i, value := range want {
		if announced[i] != value {
			t.Fatalf("announced %v, want %v", announced, want)
		}
	}
}

// TestProviderAvailabilityBeforeBind covers the constructor window: state set
// before a setter exists is remembered, not announced, and does not swallow the
// first real announcement of the opposite value.
func TestProviderAvailabilityBeforeBind(t *testing.T) {
	var availability providerAvailability
	availability.set(true)

	var announced []bool
	availability.bind(func(available bool) { announced = append(announced, available) })
	availability.set(true)
	if len(announced) != 0 {
		t.Fatalf("announced %v for an unchanged value, want none", announced)
	}
	availability.set(false)
	if len(announced) != 1 || announced[0] {
		t.Fatalf("announced %v, want [false]", announced)
	}
}

// TestProviderAvailabilityFollowsSampleLifecycle drives a real MGCP provider on a
// real local entity through announce -> sample -> expiry and asserts the announced
// useCaseAvailable flag follows, since that is what a consumer reads.
func TestProviderAvailabilityFollowsSampleLifecycle(t *testing.T) {
	certificate, err := shipcert.CreateCertificate("test", "test", "DE", "provider-availability")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		EEBUS:        config.EEBUSConfig{Port: 49877, Vendor: "Test", Brand: "Test", Model: "Test", Serial: "provider-availability"},
		Experimental: config.ExperimentalConfig{MGCPProvider: true},
	}
	bridge, err := eebus.NewBridgeService(cfg, certificate, eebus.NewEventBus())
	if err != nil {
		t.Fatal(err)
	}
	if err := bridge.Setup(); err != nil {
		t.Fatal(err)
	}
	defer bridge.Shutdown()

	provider := NewMGCPProvider(bridge.GridEntity(), nil, false)
	if err := provider.AddFeatures(); err != nil {
		t.Fatalf("AddFeatures: %v", err)
	}
	provider.AddUseCase()

	if available := announcedMGCPAvailability(t, bridge); available {
		t.Fatal("use case announced as available before the first sample")
	}

	now := time.Now()
	err = provider.PublishGridSnapshot(GridSnapshot{
		PowerW:   1234,
		Validity: ProviderValidity{ObservedAt: now, ValidUntil: now.Add(time.Minute)},
	})
	if err != nil {
		t.Fatalf("PublishGridSnapshot: %v", err)
	}
	if available := announcedMGCPAvailability(t, bridge); !available {
		t.Fatal("use case still announced as unavailable after a current sample")
	}

	provider.expireGridSnapshot(provider.snapshots.snapshotVersion(), now.Add(2*time.Minute))
	if available := announcedMGCPAvailability(t, bridge); available {
		t.Fatal("use case still announced as available after the sample expired")
	}
}

// announcedMGCPAvailability reads the useCaseAvailable flag the bridge actually
// puts on the wire, i.e. from nodeManagementUseCaseData rather than from provider
// state.
func announcedMGCPAvailability(t *testing.T, bridge *eebus.BridgeService) bool {
	t.Helper()

	nodeManagement := bridge.Service().LocalDevice().NodeManagement()
	if nodeManagement == nil {
		t.Fatal("local device has no node management feature")
	}
	data, ok := nodeManagement.DataCopy(model.FunctionTypeNodeManagementUseCaseData).(*model.NodeManagementUseCaseDataType)
	if !ok || data == nil {
		t.Fatal("node management use case data unavailable")
	}
	for _, information := range data.UseCaseInformation {
		for _, support := range information.UseCaseSupport {
			if support.UseCaseName == nil ||
				*support.UseCaseName != model.UseCaseNameTypeMonitoringOfGridConnectionPoint {
				continue
			}
			return support.UseCaseAvailable != nil && *support.UseCaseAvailable
		}
	}
	t.Fatal("MGCP use case is not announced at all")
	return false
}
