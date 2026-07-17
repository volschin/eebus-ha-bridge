package main

import (
	"strings"
	"testing"

	eebusapi "github.com/enbility/eebus-go/api"
	eglpc "github.com/enbility/eebus-go/usecases/eg/lpc"
)

// The modules slice is constructed before per-module setup() runs, so a
// registration must resolve its use case lazily. Capturing UseCase() in the
// slice literal registered a typed-nil use case and crashed the bridge at
// startup (v0.13.0 incident).
func TestUseCaseRegistrationResolvesLazily(t *testing.T) {
	var uc *eglpc.LPC
	reg := eebusUseCaseRegistration{
		name:    "LPC",
		useCase: func() eebusapi.UseCaseInterface { return uc },
	}

	if _, err := reg.resolve(); err == nil || !strings.Contains(err.Error(), "not initialised") {
		t.Fatalf("expected not-initialised error for typed-nil use case, got %v", err)
	}

	uc = &eglpc.LPC{}
	resolved, err := reg.resolve()
	if err != nil {
		t.Fatalf("expected lazily-set use case to resolve, got %v", err)
	}
	if resolved != eebusapi.UseCaseInterface(uc) {
		t.Fatalf("resolved use case is not the lazily-set instance")
	}
}

func TestUseCaseRegistrationWithoutResolverErrors(t *testing.T) {
	reg := eebusUseCaseRegistration{name: "LPC"}
	if _, err := reg.resolve(); err == nil || !strings.Contains(err.Error(), "no resolver") {
		t.Fatalf("expected no-resolver error, got %v", err)
	}
}
