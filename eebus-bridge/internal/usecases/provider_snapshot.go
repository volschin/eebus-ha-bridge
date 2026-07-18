package usecases

import (
	"errors"
	"fmt"
	"sync"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	"github.com/enbility/eebus-go/features/server"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/util"
)

var ErrProviderClosed = errors.New("provider is closed")

type ProviderSnapshotState uint8

const (
	ProviderSnapshotEmpty ProviderSnapshotState = iota + 1
	ProviderSnapshotCurrent
	ProviderSnapshotExpired
	ProviderSnapshotClosed
)

type ProviderSnapshotDiagnostics struct {
	State      ProviderSnapshotState
	ObservedAt time.Time
	ValidUntil time.Time
}

// ProviderValidity describes when a provider sample was observed and until when
// downstream consumers may treat it as current.
type ProviderValidity struct {
	ObservedAt time.Time
	ValidUntil time.Time
	Invalid    bool
}

func (v ProviderValidity) Current(now time.Time) bool {
	return !v.Invalid && !v.ObservedAt.IsZero() && !now.Before(v.ObservedAt) && now.Before(v.ValidUntil)
}

type measurementServer interface {
	AddDescription(model.MeasurementDescriptionDataType) *model.MeasurementIdType
	UpdateDataForIds([]eebusapi.MeasurementDataForID) error
}

type serializedMeasurementPublisher struct {
	mu sync.Mutex
}

// providerSnapshotStore owns the state-only half of every provider lifecycle.
// External EEBUS writes are deliberately performed by the provider while its
// separate publish mutex is held; this store's mutex therefore never spans an
// EEBUS feature call.
type providerSnapshotStore[T any] struct {
	mu       sync.RWMutex
	snapshot *T
	version  uint64
	timer    *time.Timer
	closed   bool
}

func (s *providerSnapshotStore[T]) commit(value T, validUntil time.Time, expire func(uint64)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrProviderClosed
	}
	s.stopTimerLocked()
	s.version++
	s.snapshot = &value
	version := s.version
	delay := time.Until(validUntil)
	if delay < 0 {
		delay = 0
	}
	s.timer = time.AfterFunc(delay, func() { expire(version) })
	return nil
}

func (s *providerSnapshotStore[T]) invalidate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrProviderClosed
	}
	s.stopTimerLocked()
	s.version++
	s.snapshot = nil
	return nil
}

func (s *providerSnapshotStore[T]) current(now time.Time, clone func(T) T, valid func(T, time.Time) bool) (T, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.snapshot == nil || !valid(*s.snapshot, now) {
		var zero T
		return zero, false
	}
	return clone(*s.snapshot), true
}

func (s *providerSnapshotStore[T]) shouldExpire(version uint64, now time.Time, valid func(T, time.Time) bool) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.closed && s.version == version && s.snapshot != nil && !valid(*s.snapshot, now)
}

func (s *providerSnapshotStore[T]) clearExpired(version uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.version != version || s.snapshot == nil {
		return
	}
	s.stopTimerLocked()
	s.version++
	s.snapshot = nil
}

func (s *providerSnapshotStore[T]) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.version++
	s.snapshot = nil
	s.stopTimerLocked()
}

func (s *providerSnapshotStore[T]) snapshotVersion() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

func (s *providerSnapshotStore[T]) closedState() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}

func (s *providerSnapshotStore[T]) diagnostics(
	now time.Time,
	validity func(T) ProviderValidity,
) ProviderSnapshotDiagnostics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ProviderSnapshotDiagnostics{State: ProviderSnapshotClosed}
	}
	if s.snapshot == nil {
		return ProviderSnapshotDiagnostics{State: ProviderSnapshotEmpty}
	}
	meta := validity(*s.snapshot)
	state := ProviderSnapshotExpired
	if meta.Current(now) {
		state = ProviderSnapshotCurrent
	}
	return ProviderSnapshotDiagnostics{
		State: state, ObservedAt: meta.ObservedAt, ValidUntil: meta.ValidUntil,
	}
}

func (s *providerSnapshotStore[T]) seed(value T, version uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot = &value
	s.version = version
}

func (s *providerSnapshotStore[T]) stopTimerLocked() {
	if s.timer == nil {
		return
	}
	s.timer.Stop()
	s.timer = nil
}

type providerMeasurementValue struct {
	id    *model.MeasurementIdType
	value *float64
}

func (p *serializedMeasurementPublisher) publishValue(
	server measurementServer,
	notInitialized error,
	id *model.MeasurementIdType,
	value float64,
) error {
	return p.publishValues(server, notInitialized, providerMeasurementValue{id: id, value: &value})
}

func (p *serializedMeasurementPublisher) publishValues(
	server measurementServer,
	notInitialized error,
	values ...providerMeasurementValue,
) error {
	if server == nil {
		return notInitialized
	}
	data := make([]eebusapi.MeasurementDataForID, 0, len(values))
	for _, value := range values {
		if value.id == nil {
			return notInitialized
		}
		data = append(data, measurementDataForID(*value.id, value.value))
	}
	return p.publishData(server, data)
}

func (p *serializedMeasurementPublisher) invalidate(
	server measurementServer,
	notInitialized error,
	ids ...*model.MeasurementIdType,
) error {
	if server == nil {
		return notInitialized
	}
	data := make([]eebusapi.MeasurementDataForID, 0, len(ids))
	for _, id := range ids {
		if id == nil {
			return notInitialized
		}
		data = append(data, invalidMeasurementDataForID(*id))
	}
	return p.publishData(server, data)
}

func (p *serializedMeasurementPublisher) publishData(
	server measurementServer,
	data []eebusapi.MeasurementDataForID,
) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return server.UpdateDataForIds(data)
}

type deviceConfigurationServer interface {
	AddKeyValueDescription(model.DeviceConfigurationKeyValueDescriptionDataType) *model.DeviceConfigurationKeyIdType
	UpdateKeyValueDataForKeyId(
		model.DeviceConfigurationKeyValueDataType,
		*model.DeviceConfigurationKeyValueDataElementsType,
		model.DeviceConfigurationKeyIdType,
	) error
}

func setupProviderMeasurementServer(entity spineapi.EntityLocalInterface, label string) (measurementServer, error) {
	entity.GetOrAddFeature(model.FeatureTypeTypeMeasurement, model.RoleTypeServer)
	meas, err := server.NewMeasurement(entity)
	if err != nil {
		return nil, fmt.Errorf("[%s] creating Measurement server feature failed: %w", label, err)
	}
	return meas, nil
}

type GridSnapshot struct {
	PowerW     float64
	FeedInWh   *float64
	ConsumedWh *float64
	Validity   ProviderValidity
}

func (s GridSnapshot) clone() GridSnapshot {
	s.FeedInWh = cloneFloat64Ptr(s.FeedInWh)
	s.ConsumedWh = cloneFloat64Ptr(s.ConsumedWh)
	return s
}

type PVSnapshot struct {
	PowerW   float64
	YieldWh  *float64
	Validity ProviderValidity
}

func (s PVSnapshot) clone() PVSnapshot {
	s.YieldWh = cloneFloat64Ptr(s.YieldWh)
	return s
}

type BatterySnapshot struct {
	PowerW           float64
	ChargedWh        *float64
	DischargedWh     *float64
	StateOfChargePct *float64
	Validity         ProviderValidity
}

func (s BatterySnapshot) clone() BatterySnapshot {
	s.ChargedWh = cloneFloat64Ptr(s.ChargedWh)
	s.DischargedWh = cloneFloat64Ptr(s.DischargedWh)
	s.StateOfChargePct = cloneFloat64Ptr(s.StateOfChargePct)
	return s
}

func cloneFloat64Ptr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func measurementDataForID(id model.MeasurementIdType, value *float64) eebusapi.MeasurementDataForID {
	if value == nil {
		return invalidMeasurementDataForID(id)
	}
	return eebusapi.MeasurementDataForID{
		Data: model.MeasurementDataType{
			ValueType:  util.Ptr(model.MeasurementValueTypeTypeValue),
			Value:      model.NewScaledNumberType(*value),
			ValueState: util.Ptr(model.MeasurementValueStateTypeNormal),
		},
		Id: id,
	}
}

func invalidMeasurementDataForID(id model.MeasurementIdType) eebusapi.MeasurementDataForID {
	return eebusapi.MeasurementDataForID{
		Data: model.MeasurementDataType{
			ValueType:  util.Ptr(model.MeasurementValueTypeTypeValue),
			ValueState: util.Ptr(model.MeasurementValueStateTypeError),
		},
		Id: id,
	}
}
