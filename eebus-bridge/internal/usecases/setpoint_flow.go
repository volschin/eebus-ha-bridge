package usecases

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

type setpointState struct {
	Value    float64
	Minimum  float64
	Maximum  float64
	Step     float64
	Writable bool
}

var setpointWriteTimeout = dhwWriteTimeout

func readSetpointState(
	entity spineapi.EntityRemoteInterface,
	setpointID func(spineapi.FeatureRemoteInterface) (model.SetpointIdType, bool),
	unavailable error,
) (setpointState, model.SetpointIdType, spineapi.FeatureRemoteInterface, error) {
	remote := setpointServer(entity)
	if remote == nil {
		return setpointState{}, 0, nil, unavailable
	}
	id, ok := setpointID(remote)
	if !ok {
		return setpointState{}, 0, nil, unavailable
	}
	value, ok := setpointValue(remote, id)
	if !ok {
		return setpointState{}, 0, nil, unavailable
	}
	minimum, maximum, step, ok := setpointRange(remote, id)
	if !ok {
		return setpointState{}, 0, nil, unavailable
	}
	operation := remote.Operations()[model.FunctionTypeSetpointListData]
	return setpointState{
		Value:    value,
		Minimum:  minimum,
		Maximum:  maximum,
		Step:     step,
		Writable: operation != nil && operation.Write(),
	}, id, remote, nil
}

func validateSetpointWrite(
	state setpointState,
	value float64,
	notWritable error,
	outOfRange error,
	invalidStep error,
) error {
	if !state.Writable {
		return notWritable
	}
	if !isFinite(value) || value < state.Minimum || value > state.Maximum {
		return fmt.Errorf("%w: %.3f not in [%.3f, %.3f]", outOfRange, value, state.Minimum, state.Maximum)
	}
	steps := math.Round((value - state.Minimum) / state.Step)
	if math.Abs(state.Minimum+steps*state.Step-value) > 1e-6 {
		return fmt.Errorf("%w: %.3f with step %.3f", invalidStep, value, state.Step)
	}
	return nil
}

func writeSetpointValue(
	ctx context.Context,
	entity spineapi.EntityRemoteInterface,
	remote spineapi.FeatureRemoteInterface,
	local spineapi.FeatureLocalInterface,
	id model.SetpointIdType,
	value float64,
	label string,
	unavailable error,
	rejected error,
	refresh func(),
) error {
	if remote == nil || local == nil {
		return unavailable
	}
	data, ok := remote.DataCopy(model.FunctionTypeSetpointListData).(*model.SetpointListDataType)
	if !ok || data == nil {
		return unavailable
	}
	entries := make([]model.SetpointDataType, len(data.SetpointData))
	copy(entries, data.SetpointData)
	found := false
	for index := range entries {
		if entries[index].SetpointId != nil && *entries[index].SetpointId == id {
			entries[index].Value = model.NewScaledNumberType(value)
			found = true
			break
		}
	}
	if !found {
		return unavailable
	}

	counter, err := entity.Device().Sender().Write(
		local.Address(),
		remote.Address(),
		model.CmdType{SetpointListData: &model.SetpointListDataType{SetpointData: entries}},
	)
	if err != nil {
		return fmt.Errorf("sending %s: %w", label, err)
	}
	if counter == nil {
		return fmt.Errorf("sending %s returned no message counter", label)
	}
	result := make(chan model.ResultDataType, 1)
	if err := local.AddResponseCallback(*counter, func(message spineapi.ResponseMessage) {
		if data, ok := message.Data.(*model.ResultDataType); ok && data != nil {
			result <- *data
		}
	}); err != nil {
		return fmt.Errorf("waiting for %s result: %w", label, err)
	}

	timer := time.NewTimer(setpointWriteTimeout)
	defer timer.Stop()
	select {
	case response := <-result:
		if response.ErrorNumber != nil && *response.ErrorNumber != 0 {
			return fmt.Errorf("%w: error=%d", rejected, *response.ErrorNumber)
		}
		if refresh != nil {
			refresh()
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("timed out waiting for " + label + " result")
	}
}
