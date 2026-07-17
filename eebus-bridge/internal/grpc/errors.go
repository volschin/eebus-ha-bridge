package grpc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"

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
	log.Printf("%s failed: %v", action, err)
	switch {
	case errorsIsAny(err, classes.invalidArgument):
		return status.Error(codes.InvalidArgument, classifiedErrorMessage(action, "invalid request", err))
	case errorsIsAny(err, classes.failedPrecondition):
		return status.Error(codes.FailedPrecondition, classifiedErrorMessage(action, "rejected by device", err))
	case errorsIsAny(err, classes.notFound):
		return status.Error(codes.NotFound, classifiedErrorMessage(action, "not found", err))
	case errorsIsAny(err, classes.unavailable):
		return status.Error(codes.Unavailable, classifiedErrorMessage(action, "temporarily unavailable", err))
	case errors.Is(err, context.Canceled):
		return status.Errorf(codes.Canceled, "%s: canceled", action)
	case errors.Is(err, context.DeadlineExceeded):
		return status.Errorf(codes.DeadlineExceeded, "%s: deadline exceeded", action)
	default:
		return status.Errorf(codes.Internal, "%s failed", action)
	}
}

var (
	skiDetailPattern   = regexp.MustCompile(`(?i)\b[0-9a-f]{40}\b`)
	tokenDetailPattern = regexp.MustCompile(`(?i)token=[^\s,)]+`)
)

func classifiedErrorMessage(action string, summary string, err error) string {
	detail := redactSensitiveDetail(err.Error())
	if detail == "" {
		return fmt.Sprintf("%s: %s", action, summary)
	}
	return fmt.Sprintf("%s: %s: %s", action, summary, detail)
}

func redactSensitiveDetail(detail string) string {
	detail = skiDetailPattern.ReplaceAllString(detail, "[redacted-ski]")
	detail = tokenDetailPattern.ReplaceAllString(detail, "token=[redacted]")
	return detail
}

func redactedErrorForLog(err error) string {
	if err == nil {
		return "none"
	}
	return status.Code(err).String()
}

func errorsIsAny(err error, targets []error) bool {
	for _, target := range targets {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
