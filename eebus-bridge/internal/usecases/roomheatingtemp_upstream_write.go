package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

// upstreamRoomHeatingTemperatureWriter delegates the protocol write and
// refresh to CRHT while preserving the bridge's validation, synchronous
// result, and stable error contracts.
type upstreamRoomHeatingTemperatureWriter struct {
	client caCRHTClient
	reader roomHeatingTemperatureStateReader
}

func (w *upstreamRoomHeatingTemperatureWriter) Write(
	ctx context.Context,
	entity spineapi.EntityRemoteInterface,
	value float64,
) error {
	if w == nil || w.client == nil || w.reader == nil || entity == nil {
		return ErrRoomHeatingDataUnavailable
	}
	state, err := w.reader.State(entity)
	if err != nil {
		return err
	}
	if err := validateRoomHeatingSetpointWrite(state, value); err != nil {
		return err
	}

	return awaitRoomHeatingTemperatureWrite(ctx, func(callback roomHeatingTemperatureResultCallback) (*model.MsgCounterType, error) {
		counter, writeErr := w.client.WriteRoomAirTemperatureSetpoint(entity, value, callback)
		return counter, mapUpstreamRoomHeatingTemperatureWriteError(writeErr)
	})
}

type roomHeatingTemperatureResultCallback func(model.ResultDataType, model.MsgCounterType)
type roomHeatingTemperatureWriteCall func(roomHeatingTemperatureResultCallback) (*model.MsgCounterType, error)

var roomHeatingTemperatureWriteTimeout = 10 * time.Second

func awaitRoomHeatingTemperatureWrite(ctx context.Context, write roomHeatingTemperatureWriteCall) error {
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
		return errors.New("sending room heating setpoint returned no message counter")
	}

	timer := time.NewTimer(roomHeatingTemperatureWriteTimeout)
	defer timer.Stop()
	select {
	case response := <-result:
		if response.counter != *counter {
			return errors.New("waiting for room heating setpoint result returned unexpected message counter")
		}
		if response.data.ErrorNumber != nil && *response.data.ErrorNumber != 0 {
			err := fmt.Errorf("%w: room heating setpoint error=%d", ErrRoomHeatingRejected, *response.data.ErrorNumber)
			if response.data.Description != nil && *response.data.Description != "" {
				err = fmt.Errorf("%w (%s)", err, *response.data.Description)
			}
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("timed out waiting for room heating setpoint result")
	}
}

func mapUpstreamRoomHeatingTemperatureWriteError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, eebusapi.ErrNotSupported) {
		return fmt.Errorf("%w: %v", ErrRoomHeatingNotWritable, err)
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
			return fmt.Errorf("%w: %v", ErrRoomHeatingDataUnavailable, err)
		}
	}
	return err
}
