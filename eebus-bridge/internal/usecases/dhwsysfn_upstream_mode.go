package usecases

import (
	"context"
	"fmt"
	"slices"

	ucapi "github.com/enbility/eebus-go/usecases/api"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

// upstreamDHWOperationModeWriter delegates operation-mode writes to eebus-go
// CDSF while retaining bridge-specific validation and context handling.
// Post-write convergence is performed by the CDSF client itself, not by this
// writer.
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

	// AvailableModes holds mode types only, not the underlying mode IDs, so a
	// repeated string cannot be distinguished here from a harmless duplicate
	// relation row for the same mode. Upstream resolves the write by ID and
	// only rejects it (ErrNotSupported, mapped below) when the mode is
	// genuinely unrelated or ambiguous at that level.
	if !slices.Contains(state.AvailableModes, mode) {
		return fmt.Errorf("%w: %s", ErrDHWSysFnInvalidMode, mode)
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
