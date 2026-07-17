package grpc

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	eebusapi "github.com/enbility/eebus-go/api"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestEquivalentDomainReadErrorsAreUnavailable(t *testing.T) {
	tests := []struct {
		name     string
		mapError func(error) error
		err      error
	}{
		{"DHW", func(err error) error { return mapDHWError("reading DHW", err) }, usecases.ErrDHWDataUnavailable},
		{"HVAC", func(err error) error { return mapRoomHeatingError("reading HVAC", err) }, usecases.ErrRoomHeatingDataUnavailable},
		{"LPC", func(err error) error { return mapUsecaseError("reading LPC", err, standardUsecaseErrorClasses) }, eebusapi.ErrDataNotAvailable},
		{"Monitoring", func(err error) error { return mapUsecaseError("reading Monitoring", err, standardUsecaseErrorClasses) }, eebusapi.ErrDataNotAvailable},
		{"Monitoring metadata", func(err error) error { return mapUsecaseError("reading Monitoring", err, standardUsecaseErrorClasses) }, eebusapi.ErrMetadataNotAvailable},
		{"OHPCF", func(err error) error { return mapUsecaseError("reading OHPCF", err, standardUsecaseErrorClasses) }, eebusapi.ErrDataInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if code := status.Code(test.mapError(fmt.Errorf("wrapped: %w", test.err))); code != codes.Unavailable {
				t.Fatalf("code = %v, want Unavailable", code)
			}
		})
	}
}

func TestEquivalentDeviceWriteRejectionsAreFailedPrecondition(t *testing.T) {
	tests := []error{
		mapDHWError("writing DHW", usecases.ErrDHWRejected),
		mapRoomHeatingError("writing HVAC", usecases.ErrRoomHeatingRejected),
		mapUsecaseError("writing OHPCF", usecases.ErrOHPCFRejected, standardUsecaseErrorClasses),
	}
	for _, err := range tests {
		if code := status.Code(err); code != codes.FailedPrecondition {
			t.Fatalf("code = %v, want FailedPrecondition (%v)", code, err)
		}
	}
}

func TestUnknownInternalErrorIsSanitized(t *testing.T) {
	err := mapUsecaseError("reading data", errors.New("token=super-secret"), usecaseErrorClasses{})
	if status.Code(err) != codes.Internal || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("internal error was not sanitized: %v", err)
	}
}
