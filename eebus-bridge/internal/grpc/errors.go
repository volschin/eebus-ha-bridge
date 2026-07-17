package grpc

import (
	"context"
	"errors"

	eebusapi "github.com/enbility/eebus-go/api"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var standardUsecaseErrorClasses = usecaseErrorClasses{
	failedPrecondition: []error{
		eebusapi.ErrNotSupported,
		eebusapi.ErrUsecCaseNotSupported,
		eebusapi.ErrFunctionNotSupported,
		eebusapi.ErrOperationOnFunctionNotSupported,
		eebusapi.ErrNoCompatibleEntity,
		usecases.ErrOHPCFRejected,
	},
	notFound: []error{eebusapi.ErrEntityNotFound},
	unavailable: []error{
		eebusapi.ErrMetadataNotAvailable,
		eebusapi.ErrDataNotAvailable,
		eebusapi.ErrDataInvalid,
		eebusapi.ErrDataForMetadataKeyNotFound,
		eebusapi.ErrMissingData,
		eebusapi.ErrDeviceDisconnected,
	},
}

// usecaseErrorClasses groups a domain's sentinel errors by the gRPC status
// code they map to; sentinels not listed fall through to codes.Internal.
type usecaseErrorClasses struct {
	invalidArgument    []error
	failedPrecondition []error
	notFound           []error
	unavailable        []error
}

func mapUsecaseError(action string, err error, classes usecaseErrorClasses) error {
	switch {
	case errorsIsAny(err, classes.invalidArgument):
		return status.Errorf(codes.InvalidArgument, "%s: %v", action, err)
	case errorsIsAny(err, classes.failedPrecondition):
		return status.Errorf(codes.FailedPrecondition, "%s: %v", action, err)
	case errorsIsAny(err, classes.notFound):
		return status.Errorf(codes.NotFound, "%s: %v", action, err)
	case errorsIsAny(err, classes.unavailable):
		return status.Errorf(codes.Unavailable, "%s: temporarily unavailable", action)
	case errors.Is(err, context.Canceled):
		return status.Errorf(codes.Canceled, "%s: %v", action, err)
	case errors.Is(err, context.DeadlineExceeded):
		return status.Errorf(codes.DeadlineExceeded, "%s: %v", action, err)
	default:
		return status.Errorf(codes.Internal, "%s failed", action)
	}
}

func errorsIsAny(err error, targets []error) bool {
	for _, target := range targets {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
