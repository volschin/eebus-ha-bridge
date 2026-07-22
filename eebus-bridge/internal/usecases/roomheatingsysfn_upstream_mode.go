package usecases

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

// upstreamRoomHeatingOperationModeWriter delegates operation-mode writes to
// eebus-go CRHSF while retaining the bridge's public validation, synchronous
// result and error contracts. Post-write convergence belongs to CRHSF; this
// adapter deliberately does not issue a second request or write.
type upstreamRoomHeatingOperationModeWriter struct {
	client    caCRHSFClient
	inspector roomHeatingSystemFunctionCapabilityInspector
}

func (w *upstreamRoomHeatingOperationModeWriter) WriteOperationMode(
	ctx context.Context,
	entity spineapi.EntityRemoteInterface,
	mode string,
) error {
	if w == nil || w.client == nil || w.inspector == nil || entity == nil {
		return ErrRoomHeatingSysFnDataUnavailable
	}
	state, err := w.inspector.State(entity)
	if err != nil {
		return err
	}
	if !state.ModeWritable {
		return ErrRoomHeatingSysFnNotWritable
	}

	// Validate against CRHSF's relation-derived modes before writing. The
	// adapter separately validates against MRHSF, so disagreement between the
	// independently negotiated use cases fails closed without sending.
	modes, err := w.client.OperationModes(entity)
	if err != nil {
		return mapUpstreamRoomHeatingWriteError(err)
	}
	requested := ucapi.HvacOperationModeType(mode)
	if !slices.Contains(modes, requested) {
		return fmt.Errorf("%w: %s", ErrRoomHeatingSysFnInvalidMode, mode)
	}

	return awaitRoomHeatingSystemFunctionWrite(ctx, func(callback roomHeatingResultCallback) (*model.MsgCounterType, error) {
		counter, writeErr := w.client.WriteOperationMode(entity, requested, callback)
		return counter, mapUpstreamRoomHeatingWriteError(writeErr)
	})
}

type roomHeatingResultCallback func(model.ResultDataType, model.MsgCounterType)
type roomHeatingWriteCall func(roomHeatingResultCallback) (*model.MsgCounterType, error)

var roomHeatingSystemFunctionWriteTimeout = 10 * time.Second

func awaitRoomHeatingSystemFunctionWrite(ctx context.Context, write roomHeatingWriteCall) error {
	type writeResult struct {
		data    model.ResultDataType
		counter model.MsgCounterType
	}
	result := make(chan writeResult, 1)
	counter, err := write(func(data model.ResultDataType, counter model.MsgCounterType) {
		result <- writeResult{data: data, counter: counter}
	})
	if err != nil {
		return err
	}
	if counter == nil {
		return errors.New("sending room heating operation mode returned no message counter")
	}

	timer := time.NewTimer(roomHeatingSystemFunctionWriteTimeout)
	defer timer.Stop()
	select {
	case response := <-result:
		if response.counter != *counter {
			return errors.New("waiting for room heating operation mode result returned unexpected message counter")
		}
		if response.data.ErrorNumber != nil && *response.data.ErrorNumber != 0 {
			err := fmt.Errorf("%w: room heating operation mode error=%d", ErrRoomHeatingSysFnRejected, *response.data.ErrorNumber)
			if response.data.Description != nil && *response.data.Description != "" {
				err = fmt.Errorf("%w (%s)", err, *response.data.Description)
			}
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("timed out waiting for room heating operation mode result")
	}
}

func mapUpstreamRoomHeatingWriteError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, eebusapi.ErrNotSupported) {
		return fmt.Errorf("%w: %v", ErrRoomHeatingSysFnNotWritable, err)
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
			return fmt.Errorf("%w: %v", ErrRoomHeatingSysFnDataUnavailable, err)
		}
	}
	return err
}
