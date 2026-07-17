package grpc

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	testValidSKI        = "682f708ceba5df9adcb9e6787ea911d9fc3ac490"
	testOtherValidSKI   = "782f708ceba5df9adcb9e6787ea911d9fc3ac490"
	testUnknownValidSKI = "882f708ceba5df9adcb9e6787ea911d9fc3ac490"
)

func TestAdapterRequestAndSKIHelpers(t *testing.T) {
	requestTests := []struct {
		name     string
		req      *pb.DeviceRequest
		wantCode codes.Code
	}{
		{name: "nil request", req: nil, wantCode: codes.InvalidArgument},
		{name: "empty request", req: &pb.DeviceRequest{}, wantCode: codes.OK},
	}
	for _, tt := range requestTests {
		t.Run(tt.name, func(t *testing.T) {
			if code := status.Code(requireDeviceRequest(tt.req)); code != tt.wantCode {
				t.Fatalf("status code = %v, want %v", code, tt.wantCode)
			}
		})
	}

	writeSKITests := []struct {
		name     string
		ski      string
		wantCode codes.Code
	}{
		{name: "empty", ski: "", wantCode: codes.InvalidArgument},
		{name: "separator-only", ski: " : - ", wantCode: codes.InvalidArgument},
		{name: "malformed", ski: "aa-bb-cc", wantCode: codes.InvalidArgument},
		{name: "valid formatted", ski: testValidSKI, wantCode: codes.OK},
	}
	for _, tt := range writeSKITests {
		t.Run(tt.name, func(t *testing.T) {
			if code := status.Code(requireWriteSKI(tt.ski)); code != tt.wantCode {
				t.Fatalf("status code = %v, want %v", code, tt.wantCode)
			}
		})
	}
}

func TestSubscribeFilteredEventsFiltersSKIAndConverts(t *testing.T) {
	bus := eebus.NewEventBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	received := make(chan string, 1)
	done := make(chan error, 1)

	go func() {
		done <- subscribeFilteredEvents(
			bus,
			&pb.DeviceRequest{Ski: testValidSKI},
			ctx,
			func(event *string) error {
				received <- *event
				cancel()
				return nil
			},
			func(event eebus.Event) (*string, bool) {
				if event.Type != "wanted" {
					return nil, false
				}
				return &event.SKI, true
			},
		)
	}()

	time.Sleep(10 * time.Millisecond)
	bus.Publish(eebus.Event{SKI: testOtherValidSKI, Type: "wanted"})
	bus.Publish(eebus.Event{SKI: testValidSKI, Type: "ignored"})
	bus.Publish(eebus.Event{SKI: testValidSKI, Type: "wanted"})

	select {
	case got := <-received:
		if got != eebus.NormalizeSKI(testValidSKI) {
			t.Fatalf("received SKI = %q, want %s", got, eebus.NormalizeSKI(testValidSKI))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for filtered event")
	}

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("stream loop error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for stream loop exit")
	}
}

func TestDebugLogfHonorsFlag(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	debugLogf(false, "hidden token=%s", "secret")
	if logs.Len() != 0 {
		t.Fatalf("debugLogf(false) wrote logs: %q", logs.String())
	}
	debugLogf(true, "visible %s", "entry")
	if !strings.Contains(logs.String(), "visible entry") {
		t.Fatalf("debugLogf(true) logs = %q, want visible entry", logs.String())
	}
}

func TestRequestedSKIForErrorDoesNotExposeValue(t *testing.T) {
	if got := requestedSKIForError(""); got != "empty" {
		t.Fatalf("requestedSKIForError(empty) = %q", got)
	}
	if got := requestedSKIForError(testValidSKI); got != "specified" {
		t.Fatalf("requestedSKIForError(specified) = %q", got)
	}
}
