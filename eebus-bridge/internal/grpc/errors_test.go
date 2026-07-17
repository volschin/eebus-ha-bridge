package grpc

import (
	"context"
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

func TestClassifiedUsecaseErrorsAreSanitized(t *testing.T) {
	secret := "token=super-secret"
	fullSKI := testValidSKI
	tests := []struct {
		name     string
		err      error
		wantCode codes.Code
	}{
		{
			name:     "invalid argument",
			err:      mapDHWError("writing DHW", fmt.Errorf("%s ski=%s: %w", secret, fullSKI, usecases.ErrDHWOutOfRange)),
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "failed precondition",
			err:      mapUsecaseError("writing OHPCF", fmt.Errorf("%s ski=%s: %w", secret, fullSKI, usecases.ErrOHPCFRejected), standardUsecaseErrorClasses),
			wantCode: codes.FailedPrecondition,
		},
		{
			name:     "not found",
			err:      mapUsecaseError("reading data", fmt.Errorf("%s ski=%s: %w", secret, fullSKI, eebusapi.ErrEntityNotFound), standardUsecaseErrorClasses),
			wantCode: codes.NotFound,
		},
		{
			name:     "canceled",
			err:      mapUsecaseError("reading data", fmt.Errorf("%s ski=%s: %w", secret, fullSKI, context.Canceled), standardUsecaseErrorClasses),
			wantCode: codes.Canceled,
		},
		{
			name:     "deadline exceeded",
			err:      mapUsecaseError("reading data", fmt.Errorf("%s ski=%s: %w", secret, fullSKI, context.DeadlineExceeded), standardUsecaseErrorClasses),
			wantCode: codes.DeadlineExceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := status.Convert(tt.err).Message()
			if code := status.Code(tt.err); code != tt.wantCode {
				t.Fatalf("code = %v, want %v (err: %v)", code, tt.wantCode, tt.err)
			}
			if strings.Contains(message, secret) || strings.Contains(message, fullSKI) {
				t.Fatalf("classified error leaked sensitive wrapper: %q", message)
			}
		})
	}
}

func TestRedactedErrorForLogDoesNotExposeWrappedMessage(t *testing.T) {
	leakyErr := fmt.Errorf("token=super-secret ski=%s: %w", testValidSKI, usecases.ErrOHPCFRejected)
	got := redactedErrorForLog(leakyErr)
	if strings.Contains(got, "super-secret") || strings.Contains(got, testValidSKI) {
		t.Fatalf("redactedErrorForLog leaked wrapped message: %q", got)
	}
}
