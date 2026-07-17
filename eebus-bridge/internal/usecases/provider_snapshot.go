package usecases

import (
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/util"
)

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

type deviceConfigurationServer interface {
	AddKeyValueDescription(model.DeviceConfigurationKeyValueDescriptionDataType) *model.DeviceConfigurationKeyIdType
	UpdateKeyValueDataForKeyId(
		model.DeviceConfigurationKeyValueDataType,
		*model.DeviceConfigurationKeyValueDataElementsType,
		model.DeviceConfigurationKeyIdType,
	) error
}

func stopProviderExpiryTimer(timer **time.Timer) {
	if *timer == nil {
		return
	}
	(*timer).Stop()
	*timer = nil
}

func scheduleProviderExpiryTimer(timer **time.Timer, validUntil time.Time, expire func()) {
	stopProviderExpiryTimer(timer)
	delay := time.Until(validUntil)
	if delay < 0 {
		delay = 0
	}
	*timer = time.AfterFunc(delay, expire)
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
