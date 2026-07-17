package usecases

import (
	"context"
	"fmt"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

func writeHvacCommand(
	ctx context.Context,
	entity spineapi.EntityRemoteInterface,
	remote spineapi.FeatureRemoteInterface,
	local spineapi.FeatureLocalInterface,
	cmd model.CmdType,
	refresh model.FunctionType,
	label string,
	rejected error,
	request func(spineapi.EntityRemoteInterface, model.FunctionType),
) error {
	counter, err := entity.Device().Sender().Write(local.Address(), remote.Address(), cmd)
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

	timer := time.NewTimer(dhwWriteTimeout)
	defer timer.Stop()
	select {
	case response := <-result:
		if response.ErrorNumber != nil && *response.ErrorNumber != 0 {
			return fmt.Errorf("%w: %s error=%d", rejected, label, *response.ErrorNumber)
		}
		if request != nil {
			request(entity, refresh)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("timed out waiting for %s result", label)
	}
}
