package usecases

import (
	"context"
	"fmt"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	cacdsf "github.com/enbility/eebus-go/usecases/ca/cdsf"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

type caCDSFClient interface {
	eebusapi.UseCaseInterface
	RemoteEntitiesScenarios() []eebusapi.RemoteEntityScenarios
	WriteCapabilities(spineapi.EntityRemoteInterface) (ucapi.DHWSystemFunctionWriteCapabilities, error)
	OperationModes(spineapi.EntityRemoteInterface) ([]ucapi.HvacOperationModeType, error)
	WriteOperationMode(
		spineapi.EntityRemoteInterface,
		ucapi.HvacOperationModeType,
		func(model.ResultDataType, model.MsgCounterType),
	) (*model.MsgCounterType, error)
	StartOneTimeDhw(
		spineapi.EntityRemoteInterface,
		func(model.ResultDataType, model.MsgCounterType),
	) (*model.MsgCounterType, error)
	StopOneTimeDhw(
		spineapi.EntityRemoteInterface,
		func(model.ResultDataType, model.MsgCounterType),
	) (*model.MsgCounterType, error)
}

type dhwSystemFunctionEntityResolver interface {
	CompatibleEntity(string) eebus.EntityResolution
}

type dhwSystemFunctionCapabilityInspector interface {
	State(spineapi.EntityRemoteInterface) (DHWSystemFunctionState, error)
}

type dhwBoostWriter interface {
	WriteBoost(context.Context, spineapi.EntityRemoteInterface, bool) error
}

type dhwOperationModeWriter interface {
	WriteOperationMode(context.Context, spineapi.EntityRemoteInterface, string) error
}

// CDSFConfigurationFacade keeps bridge entity composition and synchronous write
// adaptation separate from eebus-go's CDSF protocol semantics.
type CDSFConfigurationFacade struct {
	useCase             eebusapi.UseCaseInterface
	resolver            dhwSystemFunctionEntityResolver
	capabilityInspector dhwSystemFunctionCapabilityInspector
	boostWriter         dhwBoostWriter
	operationModeWriter dhwOperationModeWriter
}

func newCDSFConfigurationFacade(
	resolver dhwSystemFunctionEntityResolver,
	capabilityInspector dhwSystemFunctionCapabilityInspector,
	boostWriter dhwBoostWriter,
	operationModeWriter dhwOperationModeWriter,
) *CDSFConfigurationFacade {
	return &CDSFConfigurationFacade{
		resolver:            resolver,
		capabilityInspector: capabilityInspector,
		boostWriter:         boostWriter,
		operationModeWriter: operationModeWriter,
	}
}

// NewUpstreamDHWSystemFunctionConfiguration selects eebus-go CDSF for use-case
// negotiation, feature setup, capabilities, writes and post-write refreshes. A
// nil event callback deliberately keeps MDSF as the sole state/event owner.
func NewUpstreamDHWSystemFunctionConfiguration(
	localEntity spineapi.EntityLocalInterface,
) *CDSFConfigurationFacade {
	if localEntity == nil {
		return &CDSFConfigurationFacade{}
	}
	return newUpstreamDHWSystemFunctionConfiguration(cacdsf.NewCDSF(localEntity, nil))
}

func newUpstreamDHWSystemFunctionConfiguration(client caCDSFClient) *CDSFConfigurationFacade {
	if client == nil {
		return &CDSFConfigurationFacade{}
	}
	inspector := upstreamDHWSystemFunctionCapabilityInspector{client: client}
	facade := newCDSFConfigurationFacade(
		caCDSFEntityResolver{client: client},
		inspector,
		&upstreamDHWBoostWriter{client: client, inspector: inspector},
		&upstreamDHWOperationModeWriter{client: client, inspector: inspector},
	)
	facade.useCase = client
	return facade
}

// UseCase returns eebus-go's CDSF use case for service registration.
func (f *CDSFConfigurationFacade) UseCase() eebusapi.UseCaseInterface {
	if f == nil {
		return nil
	}
	return f.useCase
}

func (f *CDSFConfigurationFacade) CompatibleEntity(ski string) eebus.EntityResolution {
	if f == nil || f.resolver == nil {
		return eebus.EntityResolution{}
	}
	return f.resolver.CompatibleEntity(ski)
}

func (f *CDSFConfigurationFacade) State(entity spineapi.EntityRemoteInterface) (DHWSystemFunctionState, error) {
	if f == nil || f.capabilityInspector == nil {
		return DHWSystemFunctionState{}, ErrDHWSysFnDataUnavailable
	}
	return f.capabilityInspector.State(entity)
}

func (f *CDSFConfigurationFacade) WriteBoost(
	ctx context.Context,
	entity spineapi.EntityRemoteInterface,
	active bool,
) error {
	if f == nil || f.boostWriter == nil {
		return ErrDHWSysFnNotWritable
	}
	return f.boostWriter.WriteBoost(ctx, entity, active)
}

func (f *CDSFConfigurationFacade) WriteOperationMode(
	ctx context.Context,
	entity spineapi.EntityRemoteInterface,
	mode string,
) error {
	if f == nil || f.operationModeWriter == nil {
		return ErrDHWSysFnNotWritable
	}
	return f.operationModeWriter.WriteOperationMode(ctx, entity, mode)
}

type caCDSFEntityResolver struct {
	client caCDSFClient
}

func (r caCDSFEntityResolver) CompatibleEntity(ski string) eebus.EntityResolution {
	if r.client == nil {
		return eebus.EntityResolution{}
	}
	return compatibleEntity(r.client.RemoteEntitiesScenarios(), ski)
}

// upstreamDHWSystemFunctionCapabilityInspector only maps eebus-go's public CDSF
// contract into the existing bridge state shape. It does not inspect SPINE
// features, identifiers, relations or list data itself.
type upstreamDHWSystemFunctionCapabilityInspector struct {
	client caCDSFClient
}

func (i upstreamDHWSystemFunctionCapabilityInspector) State(
	entity spineapi.EntityRemoteInterface,
) (DHWSystemFunctionState, error) {
	if i.client == nil || entity == nil {
		return DHWSystemFunctionState{}, ErrDHWSysFnDataUnavailable
	}
	capabilities, err := i.client.WriteCapabilities(entity)
	if err != nil {
		return DHWSystemFunctionState{}, mapUpstreamDHWWriteError(err)
	}
	var availableModes []string
	if capabilities.OperationMode {
		modes, modesErr := i.client.OperationModes(entity)
		if modesErr != nil {
			return DHWSystemFunctionState{}, mapUpstreamDHWWriteError(modesErr)
		}
		availableModes = make([]string, 0, len(modes))
		for _, mode := range modes {
			availableModes = append(availableModes, string(mode))
		}
	}
	return DHWSystemFunctionState{
		BoostWritable:  capabilities.StartOneTimeDhw && capabilities.StopOneTimeDhw,
		AvailableModes: availableModes,
		ModeWritable:   capabilities.OperationMode,
	}, nil
}

type dhwResultCallback func(model.ResultDataType, model.MsgCounterType)
type dhwWriteCall func(dhwResultCallback) (*model.MsgCounterType, error)

var dhwSystemFunctionWriteTimeout = dhwWriteTimeout

// awaitDHWWrite adapts eebus-go's asynchronous CDSF result callback to the
// bridge's context-aware synchronous write contract. The buffered channel is
// required because a test double or transport may invoke the callback before
// the write method returns.
func awaitDHWWrite(ctx context.Context, label string, write dhwWriteCall) error {
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
		return fmt.Errorf("sending %s returned no message counter", label)
	}

	timer := time.NewTimer(dhwSystemFunctionWriteTimeout)
	defer timer.Stop()
	select {
	case response := <-result:
		if response.counter != *counter {
			return fmt.Errorf("waiting for %s result returned unexpected message counter", label)
		}
		if response.data.ErrorNumber != nil && *response.data.ErrorNumber != 0 {
			err := fmt.Errorf("%w: %s error=%d", ErrDHWSysFnRejected, label, *response.data.ErrorNumber)
			if response.data.Description != nil && *response.data.Description != "" {
				err = fmt.Errorf("%w (%s)", err, *response.data.Description)
			}
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("timed out waiting for %s result", label)
	}
}
