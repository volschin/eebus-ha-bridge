package usecases

import (
	"errors"
	"sync"
	"testing"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/util"
)

type recordingMeasurementServer struct {
	mu      sync.Mutex
	updates [][]eebusapi.MeasurementDataForID
	fail    error
}

func (s *recordingMeasurementServer) AddDescription(model.MeasurementDescriptionDataType) *model.MeasurementIdType {
	return nil
}

func (s *recordingMeasurementServer) UpdateDataForIds(data []eebusapi.MeasurementDataForID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	copied := append([]eebusapi.MeasurementDataForID(nil), data...)
	s.updates = append(s.updates, copied)
	if s.fail != nil {
		return s.fail
	}
	return nil
}

func (s *recordingMeasurementServer) snapshotUpdates() [][]eebusapi.MeasurementDataForID {
	s.mu.Lock()
	defer s.mu.Unlock()

	copied := make([][]eebusapi.MeasurementDataForID, len(s.updates))
	for index, update := range s.updates {
		copied[index] = append([]eebusapi.MeasurementDataForID(nil), update...)
	}
	return copied
}

type recordingDeviceConfigurationServer struct {
	calls int
	fail  error
}

func (s *recordingDeviceConfigurationServer) AddKeyValueDescription(model.DeviceConfigurationKeyValueDescriptionDataType) *model.DeviceConfigurationKeyIdType {
	return nil
}

func (s *recordingDeviceConfigurationServer) UpdateKeyValueDataForKeyId(
	model.DeviceConfigurationKeyValueDataType,
	*model.DeviceConfigurationKeyValueDataElementsType,
	model.DeviceConfigurationKeyIdType,
) error {
	s.calls++
	return s.fail
}

func assertSingleBatchIDs(t *testing.T, updates [][]eebusapi.MeasurementDataForID, ids ...model.MeasurementIdType) {
	t.Helper()

	if len(updates) != 1 {
		t.Fatalf("updates = %d, want one attempted batch", len(updates))
	}
	if len(updates[0]) != len(ids) {
		t.Fatalf("batch = %+v, want %d fields", updates[0], len(ids))
	}
	for index, id := range ids {
		if updates[0][index].Id != id {
			t.Fatalf("batch[%d].Id = %d, want %d; batch=%+v", index, updates[0][index].Id, id, updates[0])
		}
	}
}

func TestProviderValidityCurrentUsesExplicitClock(t *testing.T) {
	observedAt := time.Unix(100, 0)
	validity := ProviderValidity{
		ObservedAt: observedAt,
		ValidUntil: observedAt.Add(2 * time.Minute),
	}

	if !validity.Current(observedAt.Add(time.Minute)) {
		t.Fatal("Current() = false before valid_until, want true")
	}
	if validity.Current(observedAt.Add(-time.Second)) {
		t.Fatal("Current() = true before observed_at, want false")
	}
	if validity.Current(observedAt.Add(2 * time.Minute)) {
		t.Fatal("Current() = true at valid_until, want false")
	}
	if validity.Current(observedAt.Add(3 * time.Minute)) {
		t.Fatal("Current() = true after valid_until, want false")
	}
}

func TestProviderValidityInvalidSampleIsNeverCurrent(t *testing.T) {
	observedAt := time.Unix(100, 0)
	validity := ProviderValidity{
		ObservedAt: observedAt,
		ValidUntil: observedAt.Add(2 * time.Minute),
		Invalid:    true,
	}

	if validity.Current(observedAt.Add(time.Minute)) {
		t.Fatal("Current() = true for invalid sample, want false")
	}
}

func TestProviderValidityRequiresObservedAt(t *testing.T) {
	validity := ProviderValidity{
		ValidUntil: time.Unix(200, 0),
	}

	if validity.Current(time.Unix(150, 0)) {
		t.Fatal("Current() = true without observed_at, want false")
	}
}

func TestGridSnapshotExpiryInvalidatesPublishedMeasurementsWithExplicitClock(t *testing.T) {
	meas := &recordingMeasurementServer{}
	powerID := model.MeasurementIdType(1)
	feedInID := model.MeasurementIdType(2)
	consumedID := model.MeasurementIdType(3)
	provider := &MGCPProvider{
		meas:       meas,
		powerID:    &powerID,
		feedInID:   &feedInID,
		consumedID: &consumedID,
	}
	observedAt := time.Now()
	feedIn := 10.0
	consumed := 20.0

	if err := provider.PublishGridSnapshot(GridSnapshot{
		PowerW:     100,
		FeedInWh:   &feedIn,
		ConsumedWh: &consumed,
		Validity: ProviderValidity{
			ObservedAt: observedAt,
			ValidUntil: observedAt.Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("PublishGridSnapshot() error = %v", err)
	}

	provider.expireGridSnapshot(provider.snapshotVersion, observedAt.Add(2*time.Hour))
	updates := meas.snapshotUpdates()
	if len(updates) != 2 || len(updates[0]) != 3 || len(updates[1]) != 3 {
		t.Fatalf("updates = %+v", updates)
	}
	for _, item := range updates[0] {
		if item.Data.ValueState == nil || *item.Data.ValueState != model.MeasurementValueStateTypeNormal {
			t.Fatalf("initial update item = %+v, want normal", item)
		}
	}
	for _, item := range updates[1] {
		if item.Data.ValueState == nil || *item.Data.ValueState != model.MeasurementValueStateTypeError {
			t.Fatalf("expiry update item = %+v, want error", item)
		}
	}
	if _, ok := provider.CurrentGridSnapshot(time.Now()); ok {
		t.Fatal("CurrentGridSnapshot() = ok after expiry, want false")
	}
}

func TestGridSnapshotExpiryIgnoresSupersededVersion(t *testing.T) {
	meas := &recordingMeasurementServer{}
	powerID := model.MeasurementIdType(1)
	feedInID := model.MeasurementIdType(2)
	consumedID := model.MeasurementIdType(3)
	provider := &MGCPProvider{
		meas:       meas,
		powerID:    &powerID,
		feedInID:   &feedInID,
		consumedID: &consumedID,
	}
	observedAt := time.Now()

	if err := provider.PublishGridSnapshot(GridSnapshot{
		PowerW: 100,
		Validity: ProviderValidity{
			ObservedAt: observedAt,
			ValidUntil: observedAt.Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("PublishGridSnapshot(old) error = %v", err)
	}
	oldVersion := provider.snapshotVersion
	if err := provider.PublishGridSnapshot(GridSnapshot{
		PowerW: 200,
		Validity: ProviderValidity{
			ObservedAt: observedAt.Add(time.Minute),
			ValidUntil: observedAt.Add(2 * time.Hour),
		},
	}); err != nil {
		t.Fatalf("PublishGridSnapshot(new) error = %v", err)
	}

	provider.expireGridSnapshot(oldVersion, observedAt.Add(90*time.Minute))
	updates := meas.snapshotUpdates()
	if len(updates) != 2 {
		t.Fatalf("updates = %d, want only two publish batches and no stale expiry invalidation", len(updates))
	}
	if snapshot, ok := provider.CurrentGridSnapshot(observedAt.Add(90 * time.Minute)); !ok || snapshot.PowerW != 200 {
		t.Fatalf("CurrentGridSnapshot() = %+v, %v; want new snapshot", snapshot, ok)
	}
	provider.snapshotMu.Lock()
	stopProviderExpiryTimer(&provider.expiryTimer)
	provider.snapshotMu.Unlock()
}

func TestPVSnapshotDoesNotTouchStaticPeakPowerConfiguration(t *testing.T) {
	meas := &recordingMeasurementServer{}
	devConf := &recordingDeviceConfigurationServer{fail: errors.New("device configuration failed")}
	powerID := model.MeasurementIdType(1)
	yieldID := model.MeasurementIdType(2)
	peakID := model.DeviceConfigurationKeyIdType(3)
	provider := &VAPDProvider{
		meas:    meas,
		devConf: devConf,
		powerID: &powerID,
		yieldID: &yieldID,
		peakID:  &peakID,
	}
	observedAt := time.Now()

	err := provider.PublishPVSnapshot(PVSnapshot{
		PowerW: 1000,
		Validity: ProviderValidity{
			ObservedAt: observedAt,
			ValidUntil: observedAt.Add(time.Hour),
		},
	})
	if err != nil {
		t.Fatalf("PublishPVSnapshot() error = %v, want nil", err)
	}
	if devConf.calls != 0 {
		t.Fatalf("device configuration calls = %d, want 0", devConf.calls)
	}
	if updates := meas.snapshotUpdates(); len(updates) != 1 || len(updates[0]) != 2 {
		t.Fatalf("measurement updates = %+v, want one live measurement batch", updates)
	}
	if snapshot, ok := provider.CurrentPVSnapshot(time.Now()); !ok || snapshot.PowerW != 1000 {
		t.Fatalf("CurrentPVSnapshot() = %+v, %v; want committed live snapshot", snapshot, ok)
	}
	provider.snapshotMu.Lock()
	stopProviderExpiryTimer(&provider.expiryTimer)
	provider.snapshotMu.Unlock()
}

func TestGridSnapshotPublishFailureKeepsPreviousSnapshotAndUsesSingleFullBatch(t *testing.T) {
	meas := &recordingMeasurementServer{fail: errors.New("second field failed")}
	powerID := model.MeasurementIdType(1)
	feedInID := model.MeasurementIdType(2)
	consumedID := model.MeasurementIdType(3)
	now := time.Now()
	previousFeedIn := 5.0
	provider := &MGCPProvider{
		meas:       meas,
		powerID:    &powerID,
		feedInID:   &feedInID,
		consumedID: &consumedID,
		snapshot: &GridSnapshot{
			PowerW:   50,
			FeedInWh: &previousFeedIn,
			Validity: ProviderValidity{
				ObservedAt: now,
				ValidUntil: now.Add(time.Hour),
			},
		},
		snapshotVersion: 1,
	}
	feedIn := 10.0
	consumed := 20.0

	err := provider.PublishGridSnapshot(GridSnapshot{
		PowerW:     100,
		FeedInWh:   &feedIn,
		ConsumedWh: &consumed,
		Validity: ProviderValidity{
			ObservedAt: now,
			ValidUntil: now.Add(time.Hour),
		},
	})
	if err == nil {
		t.Fatal("PublishGridSnapshot() error = nil, want failure")
	}
	assertSingleBatchIDs(t, meas.snapshotUpdates(), powerID, feedInID, consumedID)
	if snapshot, ok := provider.CurrentGridSnapshot(now); !ok || snapshot.PowerW != 50 || snapshot.FeedInWh == nil || *snapshot.FeedInWh != previousFeedIn {
		t.Fatalf("CurrentGridSnapshot() = %+v, %v; want previous snapshot", snapshot, ok)
	}
}

func TestPVSnapshotPublishFailureKeepsPreviousSnapshotAndUsesSingleFullBatch(t *testing.T) {
	meas := &recordingMeasurementServer{fail: errors.New("yield field failed")}
	powerID := model.MeasurementIdType(1)
	yieldID := model.MeasurementIdType(2)
	now := time.Now()
	previousYield := 5.0
	provider := &VAPDProvider{
		meas:    meas,
		powerID: &powerID,
		yieldID: &yieldID,
		snapshot: &PVSnapshot{
			PowerW:  50,
			YieldWh: &previousYield,
			Validity: ProviderValidity{
				ObservedAt: now,
				ValidUntil: now.Add(time.Hour),
			},
		},
		snapshotVersion: 1,
	}
	yieldWh := 10.0

	err := provider.PublishPVSnapshot(PVSnapshot{
		PowerW:  100,
		YieldWh: &yieldWh,
		Validity: ProviderValidity{
			ObservedAt: now,
			ValidUntil: now.Add(time.Hour),
		},
	})
	if err == nil {
		t.Fatal("PublishPVSnapshot() error = nil, want failure")
	}
	assertSingleBatchIDs(t, meas.snapshotUpdates(), powerID, yieldID)
	if snapshot, ok := provider.CurrentPVSnapshot(now); !ok || snapshot.PowerW != 50 || snapshot.YieldWh == nil || *snapshot.YieldWh != previousYield {
		t.Fatalf("CurrentPVSnapshot() = %+v, %v; want previous snapshot", snapshot, ok)
	}
}

func TestBatterySnapshotPublishFailureKeepsPreviousSnapshotAndUsesSingleFullBatch(t *testing.T) {
	meas := &recordingMeasurementServer{fail: errors.New("soc field failed")}
	powerID := model.MeasurementIdType(1)
	chargedID := model.MeasurementIdType(2)
	dischargedID := model.MeasurementIdType(3)
	socID := model.MeasurementIdType(4)
	now := time.Now()
	previousSOC := 80.0
	provider := &VABDProvider{
		meas:         meas,
		powerID:      &powerID,
		chargedID:    &chargedID,
		dischargedID: &dischargedID,
		socID:        &socID,
		snapshot: &BatterySnapshot{
			PowerW:           50,
			StateOfChargePct: &previousSOC,
			Validity: ProviderValidity{
				ObservedAt: now,
				ValidUntil: now.Add(time.Hour),
			},
		},
		snapshotVersion: 1,
	}
	charged := 10.0
	discharged := 20.0
	soc := 90.0

	err := provider.PublishBatterySnapshot(BatterySnapshot{
		PowerW:           100,
		ChargedWh:        &charged,
		DischargedWh:     &discharged,
		StateOfChargePct: &soc,
		Validity: ProviderValidity{
			ObservedAt: now,
			ValidUntil: now.Add(time.Hour),
		},
	})
	if err == nil {
		t.Fatal("PublishBatterySnapshot() error = nil, want failure")
	}
	assertSingleBatchIDs(t, meas.snapshotUpdates(), powerID, chargedID, dischargedID, socID)
	if snapshot, ok := provider.CurrentBatterySnapshot(now); !ok || snapshot.PowerW != 50 || snapshot.StateOfChargePct == nil || *snapshot.StateOfChargePct != previousSOC {
		t.Fatalf("CurrentBatterySnapshot() = %+v, %v; want previous snapshot", snapshot, ok)
	}
}

func TestGridSnapshotCommitAndCurrentAreDeepCopies(t *testing.T) {
	meas := &recordingMeasurementServer{}
	powerID := model.MeasurementIdType(1)
	feedInID := model.MeasurementIdType(2)
	consumedID := model.MeasurementIdType(3)
	now := time.Now()
	provider := &MGCPProvider{
		meas:       meas,
		powerID:    &powerID,
		feedInID:   &feedInID,
		consumedID: &consumedID,
	}
	feedIn := 10.0

	if err := provider.PublishGridSnapshot(GridSnapshot{
		PowerW:   100,
		FeedInWh: &feedIn,
		Validity: ProviderValidity{
			ObservedAt: now,
			ValidUntil: now.Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("PublishGridSnapshot() error = %v", err)
	}
	feedIn = 999
	snapshot, ok := provider.CurrentGridSnapshot(now)
	if !ok || snapshot.FeedInWh == nil || *snapshot.FeedInWh != 10 {
		t.Fatalf("CurrentGridSnapshot() after request mutation = %+v, %v", snapshot, ok)
	}
	*snapshot.FeedInWh = 777
	snapshot, ok = provider.CurrentGridSnapshot(now)
	if !ok || snapshot.FeedInWh == nil || *snapshot.FeedInWh != 10 {
		t.Fatalf("CurrentGridSnapshot() after return mutation = %+v, %v", snapshot, ok)
	}
	provider.snapshotMu.Lock()
	stopProviderExpiryTimer(&provider.expiryTimer)
	provider.snapshotMu.Unlock()
}

func TestMeasurementDataForIDMarksOmittedFieldsInvalid(t *testing.T) {
	item := measurementDataForID(1, nil)

	if item.Data.ValueState == nil || *item.Data.ValueState != model.MeasurementValueStateTypeError {
		t.Fatalf("ValueState = %v, want error", item.Data.ValueState)
	}
	if item.Data.Value != nil {
		t.Fatalf("Value = %v, want nil", item.Data.Value)
	}
	if item.Data.ValueType == nil || *item.Data.ValueType != model.MeasurementValueTypeTypeValue {
		t.Fatalf("ValueType = %v, want value", item.Data.ValueType)
	}
}

func TestMeasurementDataForIDMarksPresentFieldsNormal(t *testing.T) {
	item := measurementDataForID(1, util.Ptr(42.0))

	if item.Data.ValueState == nil || *item.Data.ValueState != model.MeasurementValueStateTypeNormal {
		t.Fatalf("ValueState = %v, want normal", item.Data.ValueState)
	}
	if item.Data.Value == nil {
		t.Fatal("Value = nil, want scaled number")
	}
}
