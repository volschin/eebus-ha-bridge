package grpc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// usecaseErrorClasses groups a domain's sentinel errors by the gRPC status
// code they map to; sentinels not listed fall through to codes.Internal.
type usecaseErrorClasses struct {
	invalidArgument    []error
	failedPrecondition []error
	notFound           []error
}

func mapUsecaseError(action string, err error, classes usecaseErrorClasses) error {
	switch {
	case errorsIsAny(err, classes.invalidArgument):
		return status.Errorf(codes.InvalidArgument, "%s: %v", action, err)
	case errorsIsAny(err, classes.failedPrecondition):
		return status.Errorf(codes.FailedPrecondition, "%s: %v", action, err)
	case errorsIsAny(err, classes.notFound):
		return status.Errorf(codes.NotFound, "%s: %v", action, err)
	case errors.Is(err, context.Canceled):
		return status.Errorf(codes.Canceled, "%s: %v", action, err)
	case errors.Is(err, context.DeadlineExceeded):
		return status.Errorf(codes.DeadlineExceeded, "%s: %v", action, err)
	default:
		return status.Errorf(codes.Internal, "%s: %v", action, err)
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
