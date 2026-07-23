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

// TestVisualizationProviderAvailabilityFollowsSampleLifecycle is the VAPD/VABD
// half of the lifecycle assertion: both providers announce, publish and expire
// through the same helper, so the flag has to follow for each of them.
func TestVisualizationProviderAvailabilityFollowsSampleLifecycle(t *testing.T) {
	certificate, err := shipcert.CreateCertificate("test", "test", "DE", "provider-availability-vis")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		EEBUS: config.EEBUSConfig{
			Port: 49878, Vendor: "Test", Brand: "Test", Model: "Test", Serial: "provider-availability-vis",
		},
		Experimental: config.ExperimentalConfig{VAPDProvider: true, VABDProvider: true},
	}
	bridge, err := eebus.NewBridgeService(cfg, certificate, eebus.NewEventBus())
	if err != nil {
		t.Fatal(err)
	}
	if err := bridge.Setup(); err != nil {
		t.Fatal(err)
	}
	defer bridge.Shutdown()

	now := time.Now()
	validity := ProviderValidity{ObservedAt: now, ValidUntil: now.Add(time.Minute)}

	vapd := NewVAPDProvider(bridge.PVEntity(), nil, false)
	if err := vapd.AddFeatures(); err != nil {
		t.Fatalf("VAPD AddFeatures: %v", err)
	}
	vapd.AddUseCase()
	if announcedAvailability(t, bridge, model.UseCaseNameTypeVisualizationOfAggregatedPhotovoltaicData) {
		t.Fatal("VAPD announced as available before the first sample")
	}
	if err := vapd.PublishPVSnapshot(PVSnapshot{PowerW: 500, Validity: validity}); err != nil {
		t.Fatalf("PublishPVSnapshot: %v", err)
	}
	if !announcedAvailability(t, bridge, model.UseCaseNameTypeVisualizationOfAggregatedPhotovoltaicData) {
		t.Fatal("VAPD still unavailable after a current sample")
	}
	vapd.expirePVSnapshot(vapd.snapshots.snapshotVersion(), now.Add(2*time.Minute))
	if announcedAvailability(t, bridge, model.UseCaseNameTypeVisualizationOfAggregatedPhotovoltaicData) {
		t.Fatal("VAPD still available after the sample expired")
	}

	vabd := NewVABDProvider(bridge.BatteryEntity(), nil, false)
	if err := vabd.AddFeatures(); err != nil {
		t.Fatalf("VABD AddFeatures: %v", err)
	}
	vabd.AddUseCase()
	if announcedAvailability(t, bridge, model.UseCaseNameTypeVisualizationOfAggregatedBatteryData) {
		t.Fatal("VABD announced as available before the first sample")
	}
	if err := vabd.PublishBatterySnapshot(BatterySnapshot{PowerW: -300, Validity: validity}); err != nil {
		t.Fatalf("PublishBatterySnapshot: %v", err)
	}
	if !announcedAvailability(t, bridge, model.UseCaseNameTypeVisualizationOfAggregatedBatteryData) {
		t.Fatal("VABD still unavailable after a current sample")
	}
	vabd.expireBatterySnapshot(vabd.snapshots.snapshotVersion(), now.Add(2*time.Minute))
	if announcedAvailability(t, bridge, model.UseCaseNameTypeVisualizationOfAggregatedBatteryData) {
		t.Fatal("VABD still available after the sample expired")
	}

	// Both providers must also follow an explicitly invalidated source, and VABD
	// must end up unavailable once closed.
	if err := vapd.PublishPVSnapshot(PVSnapshot{PowerW: 500, Validity: validity}); err != nil {
		t.Fatalf("PublishPVSnapshot: %v", err)
	}
	if err := vapd.PublishPVSnapshot(PVSnapshot{Validity: ProviderValidity{Invalid: true}}); err != nil {
		t.Fatalf("PublishPVSnapshot (invalid): %v", err)
	}
	if announcedAvailability(t, bridge, model.UseCaseNameTypeVisualizationOfAggregatedPhotovoltaicData) {
		t.Fatal("VAPD still available after the source was reported invalid")
	}

	if err := vabd.PublishBatterySnapshot(BatterySnapshot{PowerW: -300, Validity: validity}); err != nil {
		t.Fatalf("PublishBatterySnapshot: %v", err)
	}
	if err := vabd.PublishBatterySnapshot(BatterySnapshot{Validity: ProviderValidity{Invalid: true}}); err != nil {
		t.Fatalf("PublishBatterySnapshot (invalid): %v", err)
	}
	if announcedAvailability(t, bridge, model.UseCaseNameTypeVisualizationOfAggregatedBatteryData) {
		t.Fatal("VABD still available after the source was reported invalid")
	}

	if err := vabd.PublishBatterySnapshot(BatterySnapshot{PowerW: -300, Validity: validity}); err != nil {
		t.Fatalf("PublishBatterySnapshot: %v", err)
	}
	if err := vabd.Close(); err != nil {
		t.Fatalf("VABD Close: %v", err)
	}
	if announcedAvailability(t, bridge, model.UseCaseNameTypeVisualizationOfAggregatedBatteryData) {
		t.Fatal("VABD still available after the provider was closed")
	}
}

// TestProviderAvailabilityFollowsInvalidatedSample covers the third path into
// unavailable: Home Assistant reporting the source as invalid rather than the
// sample ageing out.
func TestProviderAvailabilityFollowsInvalidatedSample(t *testing.T) {
	certificate, err := shipcert.CreateCertificate("test", "test", "DE", "provider-availability-invalid")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		EEBUS: config.EEBUSConfig{
			Port: 49879, Vendor: "Test", Brand: "Test", Model: "Test", Serial: "provider-availability-invalid",
		},
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

	now := time.Now()
	err = provider.PublishGridSnapshot(GridSnapshot{
		PowerW:   42,
		Validity: ProviderValidity{ObservedAt: now, ValidUntil: now.Add(time.Minute)},
	})
	if err != nil {
		t.Fatalf("PublishGridSnapshot: %v", err)
	}
	if !announcedMGCPAvailability(t, bridge) {
		t.Fatal("still unavailable after a current sample")
	}

	err = provider.PublishGridSnapshot(GridSnapshot{Validity: ProviderValidity{Invalid: true}})
	if err != nil {
		t.Fatalf("PublishGridSnapshot (invalid): %v", err)
	}
	if announcedMGCPAvailability(t, bridge) {
		t.Fatal("still available after the source was reported invalid")
	}
}

// announcedMGCPAvailability reads the useCaseAvailable flag the bridge actually
// puts on the wire, i.e. from nodeManagementUseCaseData rather than from provider
// state.
func announcedMGCPAvailability(t *testing.T, bridge *eebus.BridgeService) bool {
	t.Helper()
	return announcedAvailability(t, bridge, model.UseCaseNameTypeMonitoringOfGridConnectionPoint)
}

// announcedAvailability reads the useCaseAvailable flag for one use case out of
// nodeManagementUseCaseData, i.e. the value a consumer actually sees.
func announcedAvailability(
	t *testing.T,
	bridge *eebus.BridgeService,
	useCase model.UseCaseNameType,
) bool {
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
			if support.UseCaseName == nil || *support.UseCaseName != useCase {
				continue
			}
			return support.UseCaseAvailable != nil && *support.UseCaseAvailable
		}
	}
	t.Fatalf("use case %s is not announced at all", useCase)
	return false
}
