package usecases

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/stretchr/testify/mock"
)

func newRoomHeatingWriteFeature(t *testing.T) *mocks.FeatureRemoteInterface {
	t.Helper()
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(
		&model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointListData).Return(
		&model.SetpointListDataType{SetpointData: []model.SetpointDataType{
			{SetpointId: ptr(model.SetpointIdType(0)), Value: model.NewScaledNumberType(12)},
			{SetpointId: ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(21)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointConstraintsListData).Return(
		&model.SetpointConstraintsListDataType{SetpointConstraintsData: []model.SetpointConstraintsDataType{
			{
				SetpointId:       ptr(model.SetpointIdType(1)),
				SetpointRangeMin: model.NewScaledNumberType(5),
				SetpointRangeMax: model.NewScaledNumberType(30),
				SetpointStepSize: model.NewScaledNumberType(0.5),
			},
		}},
	)
	operation := mocks.NewOperationsInterface(t)
	operation.On("Write").Return(true)
	operation.On("Read").Return(true).Maybe()
	feature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{
		model.FunctionTypeSetpointListData: operation,
	})
	feature.On("Address").Return(&model.FeatureAddressType{})
	feature.On("String").Return("remote room setpoint").Maybe()
	return feature
}

func roomHeatingWriteHarness(
	t *testing.T,
	remote *mocks.FeatureRemoteInterface,
	errno *model.ErrorNumberType,
) (*mocks.EntityLocalInterface, *mocks.EntityRemoteInterface, **model.SetpointListDataType) {
	t.Helper()
	localAddress := &model.FeatureAddressType{}
	local := mocks.NewFeatureLocalInterface(t)
	local.On("Address").Return(localAddress)
	response := local.On("AddResponseCallback", mock.Anything, mock.Anything)
	if errno != nil {
		response.Run(func(args mock.Arguments) {
			callback := args.Get(1).(func(spineapi.ResponseMessage))
			callback(spineapi.ResponseMessage{Data: &model.ResultDataType{ErrorNumber: errno}})
		})
	}
	response.Return(nil)
	local.On("RequestRemoteData", mock.Anything, mock.Anything, mock.Anything, remote).
		Return(ptr(model.MsgCounterType(10)), (*model.ErrorType)(nil)).Maybe()

	var written *model.SetpointListDataType
	counter := model.MsgCounterType(9)
	sender := mocks.NewSenderInterface(t)
	sender.On("Write", localAddress, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		written = args.Get(2).(model.CmdType).SetpointListData
	}).Return(&counter, nil)
	device := mocks.NewDeviceRemoteInterface(t)
	device.On("Sender").Return(sender)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).Return(remote)
	entity.On("Device").Return(device)
	localEntity := mocks.NewEntityLocalInterface(t)
	localEntity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeClient).Return(local)
	return localEntity, entity, &written
}

func TestRoomHeatingStateUsesScopedSetpointAndDeviceConstraints(t *testing.T) {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(
		&model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointListData).Return(
		&model.SetpointListDataType{SetpointData: []model.SetpointDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(21)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointConstraintsListData).Return(
		&model.SetpointConstraintsListDataType{SetpointConstraintsData: []model.SetpointConstraintsDataType{
			{
				SetpointId:       ptr(model.SetpointIdType(1)),
				SetpointRangeMin: model.NewScaledNumberType(5),
				SetpointRangeMax: model.NewScaledNumberType(30),
				SetpointStepSize: model.NewScaledNumberType(0.5),
			},
		}},
	)
	operation := mocks.NewOperationsInterface(t)
	operation.On("Write").Return(true)
	feature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{
		model.FunctionTypeSetpointListData: operation,
	})

	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).Return(feature)

	state, err := (&RoomHeatingTemperature{}).State(entity)
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if state.Value != 21 || state.Minimum != 5 || state.Maximum != 30 || state.Step != 0.5 || !state.Writable {
		t.Errorf("State() = %+v", state)
	}
}

func TestRoomHeatingStateFailsClosedWithoutConstraints(t *testing.T) {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(
		&model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointListData).Return(
		&model.SetpointListDataType{SetpointData: []model.SetpointDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(21)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointConstraintsListData).Return(nil)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).Return(feature)

	_, err := (&RoomHeatingTemperature{}).State(entity)
	if !errors.Is(err, ErrRoomHeatingDataUnavailable) {
		t.Fatalf("State() error = %v, want ErrRoomHeatingDataUnavailable", err)
	}
}

func TestRoomHeatingStateFailsClosedOnIncompleteValueOrConstraints(t *testing.T) {
	validValue := model.NewScaledNumberType(21)
	validMinimum := model.NewScaledNumberType(5)
	validMaximum := model.NewScaledNumberType(30)
	validStep := model.NewScaledNumberType(0.5)
	tests := []struct {
		name    string
		value   *model.ScaledNumberType
		minimum *model.ScaledNumberType
		maximum *model.ScaledNumberType
		step    *model.ScaledNumberType
	}{
		{name: "missing value", minimum: validMinimum, maximum: validMaximum, step: validStep},
		{name: "missing minimum", value: validValue, maximum: validMaximum, step: validStep},
		{name: "missing maximum", value: validValue, minimum: validMinimum, step: validStep},
		{name: "missing step", value: validValue, minimum: validMinimum, maximum: validMaximum},
		{name: "zero step", value: validValue, minimum: validMinimum, maximum: validMaximum, step: model.NewScaledNumberType(0)},
		{name: "inverted range", value: validValue, minimum: validMaximum, maximum: validMinimum, step: validStep},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			feature := mocks.NewFeatureRemoteInterface(t)
			feature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(
				&model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
					{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
				}},
			)
			feature.On("DataCopy", model.FunctionTypeSetpointListData).Return(
				&model.SetpointListDataType{SetpointData: []model.SetpointDataType{
					{SetpointId: ptr(model.SetpointIdType(1)), Value: test.value},
				}},
			).Maybe()
			feature.On("DataCopy", model.FunctionTypeSetpointConstraintsListData).Return(
				&model.SetpointConstraintsListDataType{SetpointConstraintsData: []model.SetpointConstraintsDataType{
					{
						SetpointId:       ptr(model.SetpointIdType(1)),
						SetpointRangeMin: test.minimum,
						SetpointRangeMax: test.maximum,
						SetpointStepSize: test.step,
					},
				}},
			).Maybe()
			entity := mocks.NewEntityRemoteInterface(t)
			entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).Return(feature)

			_, err := (&RoomHeatingTemperature{}).State(entity)
			if !errors.Is(err, ErrRoomHeatingDataUnavailable) {
				t.Fatalf("State() error = %v, want ErrRoomHeatingDataUnavailable", err)
			}
		})
	}
}

func TestRoomHeatingSetpointIDRequiresOneDistinctScopedCandidate(t *testing.T) {
	tests := []struct {
		name         string
		descriptions []model.SetpointDescriptionDataType
		wantID       model.SetpointIdType
		wantOK       bool
	}{
		{name: "missing"},
		{
			name: "one room-air setpoint",
			descriptions: []model.SetpointDescriptionDataType{
				{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
				{SetpointId: ptr(model.SetpointIdType(2)), ScopeType: ptr(model.ScopeTypeTypeDhwTemperature)},
			},
			wantID: 1,
			wantOK: true,
		},
		{
			name: "duplicate description for same ID",
			descriptions: []model.SetpointDescriptionDataType{
				{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
				{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
			},
			wantID: 1,
			wantOK: true,
		},
		{
			name: "distinct room-air setpoints are ambiguous",
			descriptions: []model.SetpointDescriptionDataType{
				{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
				{SetpointId: ptr(model.SetpointIdType(2)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			feature := mocks.NewFeatureRemoteInterface(t)
			feature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(
				&model.SetpointDescriptionListDataType{SetpointDescriptionData: test.descriptions},
			)

			id, ok := roomHeatingSetpointID(feature)
			if ok != test.wantOK || id != test.wantID {
				t.Fatalf("roomHeatingSetpointID() = (%d, %t), want (%d, %t)", id, ok, test.wantID, test.wantOK)
			}
		})
	}
}

func TestRoomHeatingStateFailsClosedOnAmbiguousScopedSetpoints(t *testing.T) {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(
		&model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
			{SetpointId: ptr(model.SetpointIdType(2)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
		}},
	)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).Return(feature)

	_, err := (&RoomHeatingTemperature{}).State(entity)
	if !errors.Is(err, ErrRoomHeatingDataUnavailable) {
		t.Fatalf("State() error = %v, want ErrRoomHeatingDataUnavailable", err)
	}
}

func TestRoomHeatingWriteRejectsOutOfRangeValue(t *testing.T) {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(
		&model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointListData).Return(
		&model.SetpointListDataType{SetpointData: []model.SetpointDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(21)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointConstraintsListData).Return(
		&model.SetpointConstraintsListDataType{SetpointConstraintsData: []model.SetpointConstraintsDataType{
			{
				SetpointId:       ptr(model.SetpointIdType(1)),
				SetpointRangeMin: model.NewScaledNumberType(5),
				SetpointRangeMax: model.NewScaledNumberType(30),
				SetpointStepSize: model.NewScaledNumberType(0.5),
			},
		}},
	)
	operation := mocks.NewOperationsInterface(t)
	operation.On("Write").Return(true)
	feature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{
		model.FunctionTypeSetpointListData: operation,
	})
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).Return(feature)

	err := (&RoomHeatingTemperature{}).Write(context.Background(), entity, 35)
	if !errors.Is(err, ErrRoomHeatingOutOfRange) {
		t.Fatalf("Write() error = %v, want ErrRoomHeatingOutOfRange", err)
	}
}

func TestRoomHeatingWriteRejectsOffStepValue(t *testing.T) {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(
		&model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointListData).Return(
		&model.SetpointListDataType{SetpointData: []model.SetpointDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(21)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointConstraintsListData).Return(
		&model.SetpointConstraintsListDataType{SetpointConstraintsData: []model.SetpointConstraintsDataType{
			{
				SetpointId:       ptr(model.SetpointIdType(1)),
				SetpointRangeMin: model.NewScaledNumberType(5),
				SetpointRangeMax: model.NewScaledNumberType(30),
				SetpointStepSize: model.NewScaledNumberType(0.5),
			},
		}},
	)
	operation := mocks.NewOperationsInterface(t)
	operation.On("Write").Return(true)
	feature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{
		model.FunctionTypeSetpointListData: operation,
	})
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).Return(feature)

	err := (&RoomHeatingTemperature{}).Write(context.Background(), entity, 21.25)
	if !errors.Is(err, ErrRoomHeatingInvalidStep) {
		t.Fatalf("Write() error = %v, want ErrRoomHeatingInvalidStep", err)
	}
}

func TestRoomHeatingWritePreservesFullListAndAwaitsAcceptance(t *testing.T) {
	feature := newRoomHeatingWriteFeature(t)
	errno := model.ErrorNumberType(0)
	local, entity, written := roomHeatingWriteHarness(t, feature, &errno)

	if err := (&RoomHeatingTemperature{localEntity: local}).Write(context.Background(), entity, 21.5); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if *written == nil || len((*written).SetpointData) != 2 {
		t.Fatalf("written SetpointListData = %+v, want two entries", *written)
	}
	if value := (*written).SetpointData[0].Value.GetValue(); value != 12 {
		t.Errorf("unrelated setpoint = %v, want 12", value)
	}
	if value := (*written).SetpointData[1].Value.GetValue(); value != 21.5 {
		t.Errorf("room setpoint = %v, want 21.5", value)
	}
}

func TestRoomHeatingWriteResultAndWaitFailures(t *testing.T) {
	tests := []struct {
		name      string
		errno     *model.ErrorNumberType
		configure func(*testing.T) context.Context
		check     func(error) bool
	}{
		{
			name:  "device rejection",
			errno: ptr(model.ErrorNumberType(4)),
			configure: func(*testing.T) context.Context {
				return context.Background()
			},
			check: func(err error) bool { return errors.Is(err, ErrRoomHeatingRejected) },
		},
		{
			name: "caller cancellation",
			configure: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			check: func(err error) bool { return errors.Is(err, context.Canceled) },
		},
		{
			name: "internal result timeout",
			configure: func(t *testing.T) context.Context {
				previousTimeout := setpointWriteTimeout
				setpointWriteTimeout = time.Millisecond
				t.Cleanup(func() { setpointWriteTimeout = previousTimeout })
				return context.Background()
			},
			check: func(err error) bool {
				return err != nil && strings.Contains(err.Error(), "timed out waiting for room heating setpoint result")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			feature := newRoomHeatingWriteFeature(t)
			local, entity, _ := roomHeatingWriteHarness(t, feature, test.errno)
			err := (&RoomHeatingTemperature{localEntity: local}).Write(test.configure(t), entity, 21.5)
			if !test.check(err) {
				t.Fatalf("Write() error = %v", err)
			}
		})
	}
}
