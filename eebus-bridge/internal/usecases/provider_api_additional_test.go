package usecases

import (
	"errors"
	"testing"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	shipcert "github.com/enbility/ship-go/cert"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/stretchr/testify/mock"
	"github.com/volschin/eebus-bridge/internal/config"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestProviderConstructorsExposeTheirUseCases(t *testing.T) {
	bus := eebus.NewEventBus()
	events := mocks.NewEventsManagerInterface(t)
	events.On("Subscribe", mock.Anything).Return(nil).Times(3)
	device := mocks.NewDeviceLocalInterface(t)
	device.On("Events").Return(events).Times(3)
	entity := mocks.NewEntityLocalInterface(t)
	entity.On("Device").Return(device).Times(3)
	providers := []eebusapi.UseCaseInterface{
		NewMGCPProvider(entity, bus, false).UseCase(),
		NewVAPDProvider(entity, bus, false).UseCase(),
		NewVABDProvider(entity, bus, false).UseCase(),
	}
	for index, provider := range providers {
		if provider == nil {
			t.Fatalf("provider %d returned a nil use case", index)
		}
	}
}

func TestProvidersAddFeaturesToRealLocalEntities(t *testing.T) {
	certificate, err := shipcert.CreateCertificate("test", "test", "DE", "provider-features")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		EEBUS:        config.EEBUSConfig{Port: 49876, Vendor: "Test", Brand: "Test", Model: "Test", Serial: "provider-features"},
		Experimental: config.ExperimentalConfig{MGCPProvider: true, VAPDProvider: true, VABDProvider: true},
	}
	bridge, err := eebus.NewBridgeService(cfg, certificate, eebus.NewEventBus())
	if err != nil {
		t.Fatal(err)
	}
	if err := bridge.Setup(); err != nil {
		t.Fatal(err)
	}
	defer bridge.Shutdown()

	mgcp := NewMGCPProvider(bridge.GridEntity(), nil, false)
	if err := mgcp.AddFeatures(); err != nil {
		t.Fatalf("MGCP AddFeatures: %v", err)
	}
	if mgcp.meas == nil || mgcp.powerID == nil || mgcp.feedInID == nil || mgcp.consumedID == nil {
		t.Fatalf("MGCP features incomplete: %+v", mgcp)
	}

	vapd := NewVAPDProvider(bridge.PVEntity(), nil, false)
	if err := vapd.AddFeatures(); err != nil {
		t.Fatalf("VAPD AddFeatures: %v", err)
	}
	if vapd.meas == nil || vapd.powerID == nil || vapd.yieldID == nil || vapd.devConf == nil || vapd.peakID == nil {
		t.Fatalf("VAPD features incomplete: %+v", vapd)
	}

	vabd := NewVABDProvider(bridge.BatteryEntity(), nil, false)
	if err := vabd.AddFeatures(); err != nil {
		t.Fatalf("VABD AddFeatures: %v", err)
	}
	if vabd.meas == nil || vabd.powerID == nil || vabd.chargedID == nil || vabd.dischargedID == nil || vabd.socID == nil {
		t.Fatalf("VABD features incomplete: %+v", vabd)
	}
}

func TestProviderIDFormattingHandlesNilAndValues(t *testing.T) {
	if got := idVal(nil); got != -1 {
		t.Fatalf("idVal(nil) = %d, want -1", got)
	}
	measurementID := model.MeasurementIdType(42)
	if got := idVal(&measurementID); got != 42 {
		t.Fatalf("idVal() = %d, want 42", got)
	}
	if got := keyIDVal(nil); got != -1 {
		t.Fatalf("keyIDVal(nil) = %d, want -1", got)
	}
	keyID := model.DeviceConfigurationKeyIdType(7)
	if got := keyIDVal(&keyID); got != 7 {
		t.Fatalf("keyIDVal() = %d, want 7", got)
	}
}

func TestLegacyProviderWritesRejectUninitializedProviders(t *testing.T) {
	checks := []struct {
		name string
		want error
		call func() error
	}{
		{"MGCP power", errMGCPNotInitialized, func() error { return (&MGCPProvider{}).PublishPower(1) }},
		{"MGCP feed-in", errMGCPNotInitialized, func() error { return (&MGCPProvider{}).PublishEnergyFeedIn(1) }},
		{"MGCP consumed", errMGCPNotInitialized, func() error { return (&MGCPProvider{}).PublishEnergyConsumed(1) }},
		{"VAPD power", errVAPDNotInitialized, func() error { return (&VAPDProvider{}).PublishPower(1) }},
		{"VAPD yield", errVAPDNotInitialized, func() error { return (&VAPDProvider{}).PublishYield(1) }},
		{"VAPD peak", errVAPDNotInitialized, func() error { return (&VAPDProvider{}).PublishPeakPower(1) }},
		{"VABD power", errVABDNotInitialized, func() error { return (&VABDProvider{}).PublishPower(1) }},
		{"VABD charged", errVABDNotInitialized, func() error { return (&VABDProvider{}).PublishEnergyCharged(1) }},
		{"VABD discharged", errVABDNotInitialized, func() error { return (&VABDProvider{}).PublishEnergyDischarged(1) }},
		{"VABD state of charge", errVABDNotInitialized, func() error { return (&VABDProvider{}).PublishStateOfCharge(1) }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); !errors.Is(err, check.want) {
				t.Fatalf("error = %v, want %v", err, check.want)
			}
		})
	}
}

func TestLegacyProviderWritesPublishEveryValue(t *testing.T) {
	t.Run("MGCP", func(t *testing.T) {
		server := &recordingMeasurementServer{}
		ids := []model.MeasurementIdType{1, 2, 3}
		provider := &MGCPProvider{meas: server, powerID: &ids[0], feedInID: &ids[1], consumedID: &ids[2], debug: true}
		for name, call := range map[string]func(float64) error{
			"power": provider.PublishPower, "feed-in": provider.PublishEnergyFeedIn, "consumed": provider.PublishEnergyConsumed,
		} {
			if err := call(12.5); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
		if updates := server.snapshotUpdates(); len(updates) != 3 {
			t.Fatalf("updates = %d, want 3", len(updates))
		}
	})

	t.Run("VAPD", func(t *testing.T) {
		server := &recordingMeasurementServer{}
		configuration := &recordingDeviceConfigurationServer{}
		ids := []model.MeasurementIdType{1, 2}
		peakID := model.DeviceConfigurationKeyIdType(3)
		provider := &VAPDProvider{meas: server, devConf: configuration, powerID: &ids[0], yieldID: &ids[1], peakID: &peakID, debug: true}
		if err := provider.PublishPower(100); err != nil {
			t.Fatal(err)
		}
		if err := provider.PublishYield(200); err != nil {
			t.Fatal(err)
		}
		if err := provider.PublishPeakPower(300); err != nil {
			t.Fatal(err)
		}
		if updates := server.snapshotUpdates(); len(updates) != 2 || configuration.calls != 1 {
			t.Fatalf("measurement updates = %d, configuration calls = %d", len(updates), configuration.calls)
		}
	})

	t.Run("VABD", func(t *testing.T) {
		server := &recordingMeasurementServer{}
		ids := []model.MeasurementIdType{1, 2, 3, 4}
		provider := &VABDProvider{
			meas: server, powerID: &ids[0], chargedID: &ids[1], dischargedID: &ids[2], socID: &ids[3], debug: true,
		}
		for name, call := range map[string]func(float64) error{
			"power": provider.PublishPower, "charged": provider.PublishEnergyCharged,
			"discharged": provider.PublishEnergyDischarged, "state of charge": provider.PublishStateOfCharge,
		} {
			if err := call(50); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
		if updates := server.snapshotUpdates(); len(updates) != 4 {
			t.Fatalf("updates = %d, want 4", len(updates))
		}
	})
}

func TestLegacyProviderWritesPropagateServerErrors(t *testing.T) {
	want := errors.New("publish failed")
	server := &recordingMeasurementServer{fail: want}
	id := model.MeasurementIdType(1)
	provider := &MGCPProvider{meas: server, powerID: &id}
	if err := provider.PublishPower(1); !errors.Is(err, want) {
		t.Fatalf("PublishPower error = %v, want %v", err, want)
	}

	configuration := &recordingDeviceConfigurationServer{fail: want}
	keyID := model.DeviceConfigurationKeyIdType(2)
	pv := &VAPDProvider{devConf: configuration, peakID: &keyID}
	if err := pv.PublishPeakPower(1); !errors.Is(err, want) {
		t.Fatalf("PublishPeakPower error = %v, want %v", err, want)
	}
}

func TestProviderSupportEventsPublishOnlyMatchingUpdates(t *testing.T) {
	tests := []struct {
		name string
		want eebus.EventType
		call func(*eebus.EventBus, eebusapi.EventType)
	}{
		{"MGCP", eebus.EventTypeMGCPConsumerUpdated, func(bus *eebus.EventBus, event eebusapi.EventType) {
			(&MGCPProvider{bus: bus}).handleEvent("ab:cd", nil, nil, event)
		}},
		{"VAPD", eebus.EventTypeVAPDConsumerUpdated, func(bus *eebus.EventBus, event eebusapi.EventType) {
			(&VAPDProvider{bus: bus}).handleEvent("ab:cd", nil, nil, event)
		}},
		{"VABD", eebus.EventTypeVABDConsumerUpdated, func(bus *eebus.EventBus, event eebusapi.EventType) {
			(&VABDProvider{bus: bus}).handleEvent("ab:cd", nil, nil, event)
		}},
	}
	matching := []eebusapi.EventType{mgcpUseCaseSupportUpdate, vapdUseCaseSupportUpdate, vabdUseCaseSupportUpdate}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bus := eebus.NewEventBus()
			channel := bus.Subscribe()
			defer bus.Unsubscribe(channel)
			test.call(bus, "unrelated")
			select {
			case event := <-channel:
				t.Fatalf("unrelated event published %+v", event)
			default:
			}
			test.call(bus, matching[index])
			select {
			case event := <-channel:
				if event.Type != test.want || event.SKI != "ABCD" {
					t.Fatalf("event = %+v, want %q for normalized SKI", event, test.want)
				}
			case <-time.After(time.Second):
				t.Fatal("timeout waiting for provider event")
			}
			test.call(nil, matching[index])
		})
	}
}

func TestProviderSnapshotDiagnosticsStates(t *testing.T) {
	now := time.Now()
	server := &recordingMeasurementServer{}
	id := model.MeasurementIdType(1)
	provider := &MGCPProvider{meas: server, powerID: &id, feedInID: &id, consumedID: &id}

	if diagnostics := provider.Diagnostics(now); diagnostics.State != ProviderSnapshotEmpty {
		t.Fatalf("empty diagnostics = %+v", diagnostics)
	}
	if err := provider.PublishGridSnapshot(GridSnapshot{PowerW: 1, Validity: ProviderValidity{ObservedAt: now, ValidUntil: now.Add(time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	if diagnostics := provider.Diagnostics(now); diagnostics.State != ProviderSnapshotCurrent || diagnostics.ObservedAt != now {
		t.Fatalf("current diagnostics = %+v", diagnostics)
	}
	if diagnostics := provider.Diagnostics(now.Add(2 * time.Hour)); diagnostics.State != ProviderSnapshotExpired {
		t.Fatalf("expired diagnostics = %+v", diagnostics)
	}
	if err := provider.Close(); err != nil {
		t.Fatal(err)
	}
	if diagnostics := provider.Diagnostics(now); diagnostics.State != ProviderSnapshotClosed {
		t.Fatalf("closed diagnostics = %+v", diagnostics)
	}
}

func TestSerializedPublisherRejectsMissingDependenciesAndInvalidates(t *testing.T) {
	publisher := &serializedMeasurementPublisher{}
	id := model.MeasurementIdType(1)
	if err := publisher.publishValues(nil, errMGCPNotInitialized, providerMeasurementValue{id: &id}); !errors.Is(err, errMGCPNotInitialized) {
		t.Fatalf("nil server error = %v", err)
	}
	server := &recordingMeasurementServer{}
	if err := publisher.publishValues(server, errMGCPNotInitialized, providerMeasurementValue{}); !errors.Is(err, errMGCPNotInitialized) {
		t.Fatalf("nil ID error = %v", err)
	}
	if err := publisher.invalidate(nil, errMGCPNotInitialized, &id); !errors.Is(err, errMGCPNotInitialized) {
		t.Fatalf("nil invalidate server error = %v", err)
	}
	if err := publisher.invalidate(server, errMGCPNotInitialized, nil); !errors.Is(err, errMGCPNotInitialized) {
		t.Fatalf("nil invalidate ID error = %v", err)
	}
	if err := publisher.invalidate(server, errMGCPNotInitialized, &id); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	updates := server.snapshotUpdates()
	if len(updates) != 1 || updates[0][0].Data.ValueState == nil || *updates[0][0].Data.ValueState != model.MeasurementValueStateTypeError {
		t.Fatalf("invalidate updates = %+v", updates)
	}
}
