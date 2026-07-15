package usecases_test

import (
	"testing"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	eebusservice "github.com/enbility/eebus-go/service"
	cemohpcf "github.com/enbility/eebus-go/usecases/cem/ohpcf"
	shipapi "github.com/enbility/ship-go/api"
	shipcert "github.com/enbility/ship-go/cert"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
)

func TestOHPCFEventRouting(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	w := usecases.NewOHPCFWrapper(bus, nil, false)
	w.HandleEvent("ohpcf-ski", nil, nil, cemohpcf.DataUpdateConsumptionState)

	select {
	case evt := <-ch:
		if evt.Type != eebus.EventTypeOHPCFConsumptionStateUpdated {
			t.Errorf("Type = %q, want ohpcf.consumption_state_updated", evt.Type)
		}
		if evt.SKI != "ohpcf-ski" {
			t.Errorf("SKI = %q, want ohpcf-ski", evt.SKI)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for ohpcf event")
	}
}

func TestOHPCFSetupIsIdempotent(t *testing.T) {
	cert, err := shipcert.CreateCertificate("test", "test", "DE", "ohpcf-test")
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	cfg, err := eebusapi.NewConfiguration(
		"test", "test", "test", "ohpcf-test",
		[]shipapi.DeviceCategoryType{shipapi.DeviceCategoryTypeEnergyManagementSystem},
		model.DeviceTypeTypeEnergyManagementSystem,
		[]model.EntityTypeType{model.EntityTypeTypeCEM},
		9876, cert, time.Second*4, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewConfiguration: %v", err)
	}

	svc := eebusservice.NewService(cfg, eebus.NewCallbacks(eebus.NewEventBus(), false))
	if err := svc.Setup(); err != nil {
		t.Fatalf("service Setup: %v", err)
	}
	localEntity := svc.LocalDevice().EntityForType(model.EntityTypeTypeCEM)
	if localEntity == nil {
		t.Fatal("local CEM entity is nil")
	}

	w := usecases.NewOHPCFWrapper(eebus.NewEventBus(), nil, false)

	w.Setup(localEntity)
	first := w.UseCase()
	if first == nil {
		t.Fatal("UseCase() is nil after first Setup")
	}

	// A second Setup must not construct a new OHPCF use case (which would also add
	// a duplicate event-bus subscription). The pointer must be unchanged.
	w.Setup(localEntity)
	second := w.UseCase()
	if first != second {
		t.Error("Setup is not idempotent: second call replaced the OHPCF use case")
	}
}
