package usecases

import (
	"context"
	"errors"
	"fmt"

	eebusapi "github.com/enbility/eebus-go/api"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

// upstreamDHWBoostWriter delegates the one-time-DHW command to eebus-go CDSF
// while retaining bridge-specific context handling and post-write convergence.
// It never falls back to the legacy writer after an upstream call.
type upstreamDHWBoostWriter struct {
	client    caCDSFClient
	inspector dhwSystemFunctionCapabilityInspector
	request   func(spineapi.EntityRemoteInterface, model.FunctionType)
}

func (w *upstreamDHWBoostWriter) WriteBoost(
	ctx context.Context,
	entity spineapi.EntityRemoteInterface,
	active bool,
) error {
	if w == nil || w.client == nil || w.inspector == nil || entity == nil {
		return ErrDHWSysFnDataUnavailable
	}
	state, err := w.inspector.State(entity)
	if err != nil {
		return err
	}
	if !state.BoostWritable {
		return ErrDHWSysFnNotWritable
	}

	label := "DHW boost stop"
	if active {
		label = "DHW boost start"
	}
	err = awaitDHWWrite(ctx, label, func(callback dhwResultCallback) (*model.MsgCounterType, error) {
		var counter *model.MsgCounterType
		var writeErr error
		if active {
			counter, writeErr = w.client.StartOneTimeDhw(entity, callback)
		} else {
			counter, writeErr = w.client.StopOneTimeDhw(entity, callback)
		}
		return counter, mapUpstreamDHWWriteError(writeErr)
	})
	if err != nil {
		return err
	}
	if w.request != nil {
		w.request(entity, model.FunctionTypeHvacOverrunListData)
	}
	return nil
}

func mapUpstreamDHWWriteError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, eebusapi.ErrNotSupported) {
		return fmt.Errorf("%w: %v", ErrDHWSysFnNotWritable, err)
	}
	for _, unavailable := range []error{
		eebusapi.ErrMetadataNotAvailable,
		eebusapi.ErrDataNotAvailable,
		eebusapi.ErrDataInvalid,
		eebusapi.ErrDataForMetadataKeyNotFound,
		eebusapi.ErrEntityNotFound,
		eebusapi.ErrMissingData,
		eebusapi.ErrDeviceDisconnected,
		eebusapi.ErrNoCompatibleEntity,
	} {
		if errors.Is(err, unavailable) {
			return fmt.Errorf("%w: %v", ErrDHWSysFnDataUnavailable, err)
		}
	}
	return err
}
