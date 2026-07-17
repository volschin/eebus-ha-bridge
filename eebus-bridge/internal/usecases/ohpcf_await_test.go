package usecases

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/enbility/spine-go/model"
)

func ptr[T any](v T) *T { return &v }

// awaitWrite must return nil when the device replies with a zero (accepted) result.
func TestAwaitWriteAccepted(t *testing.T) {
	err := awaitWrite("schedule", func(cb func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error) {
		cb(model.ResultDataType{}, model.MsgCounterType(0)) // ErrorNumber nil == success
		return ptr(model.MsgCounterType(1)), nil
	})
	if err != nil {
		t.Fatalf("awaitWrite accepted = %v, want nil", err)
	}
}

// awaitWrite must surface a device-side rejection (non-zero ErrorNumber) as an
// error carrying the action, the error code, and the description.
func TestAwaitWriteRejected(t *testing.T) {
	err := awaitWrite("schedule", func(cb func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error) {
		cb(model.ResultDataType{
			ErrorNumber: ptr(model.ErrorNumberType(7)),
			Description: ptr(model.DescriptionType("not commissioned")),
		}, model.MsgCounterType(0))
		return ptr(model.MsgCounterType(1)), nil
	})
	if err == nil {
		t.Fatal("awaitWrite rejected = nil, want error")
	}
	if !errors.Is(err, ErrOHPCFRejected) {
		t.Fatalf("awaitWrite error = %v, want ErrOHPCFRejected", err)
	}
	for _, want := range []string{"schedule", "7", "not commissioned"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

// A send-time failure (the write never reaches the device) must propagate verbatim
// and must not block waiting for a result that will never arrive.
func TestAwaitWriteSendError(t *testing.T) {
	sentinel := errors.New("send failed")
	called := false
	err := awaitWrite("pause", func(cb func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error) {
		_ = cb
		called = true
		return nil, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("awaitWrite send error = %v, want %v", err, sentinel)
	}
	if !called {
		t.Error("write closure was not invoked")
	}
}

// If the device never returns a result, awaitWrite must time out instead of
// blocking forever.
func TestAwaitWriteTimeout(t *testing.T) {
	prev := ohpcfWriteTimeout
	ohpcfWriteTimeout = 20 * time.Millisecond
	defer func() { ohpcfWriteTimeout = prev }()

	start := time.Now()
	err := awaitWrite("abort", func(cb func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error) {
		_ = cb // never invoked: simulates an unresponsive device
		return ptr(model.MsgCounterType(1)), nil
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("awaitWrite timeout = %v, want timeout error", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("awaitWrite blocked %v, expected ~timeout", elapsed)
	}
}
