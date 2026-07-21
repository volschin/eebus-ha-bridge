package usecases

import (
	"context"
	"fmt"

	ucapi "github.com/enbility/eebus-go/usecases/api"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

// upstreamDHWOperationModeWriter delegates operation-mode writes to eebus-go
// CDSF while retaining bridge-specific validation, context handling and
// post-write convergence. It never falls back to the legacy writer after an
// upstream call.
type upstreamDHWOperationModeWriter struct {
	client    caCDSFClient
	inspector dhwSystemFunctionCapabilityInspector
}

func (w *upstreamDHWOperationModeWriter) WriteOperationMode(
	ctx context.Context,
	entity spineapi.EntityRemoteInterface,
	mode string,
) error {
	if w == nil || w.client == nil || w.inspector == nil || entity == nil {
		return ErrDHWSysFnDataUnavailable
	}
	state, err := w.inspector.State(entity)
	if err != nil {
		return err
	}
	if !state.ModeWritable {
		return ErrDHWSysFnNotWritable
	}

	matches := 0
	for _, available := range state.AvailableModes {
		if available == mode {
			matches++
		}
	}
	if matches == 0 {
		return fmt.Errorf("%w: %s", ErrDHWSysFnInvalidMode, mode)
	}
	if matches != 1 {
		return ErrDHWSysFnDataUnavailable
	}

	err = awaitDHWWrite(ctx, "DHW operation mode", func(callback dhwResultCallback) (*model.MsgCounterType, error) {
		counter, writeErr := w.client.WriteOperationMode(entity, ucapi.HvacOperationModeType(mode), callback)
		return counter, mapUpstreamDHWWriteError(writeErr)
	})
	if err != nil {
		return err
	}
	return nil
}
