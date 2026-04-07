package eebus

import (
	"crypto/tls"
	"fmt"
	"time"

	"github.com/enbility/eebus-go/api"
	eebusservice "github.com/enbility/eebus-go/service"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/config"
)

// BridgeService wraps eebus-go Service with bridge-specific lifecycle.
type BridgeService struct {
	service   *eebusservice.Service
	callbacks *Callbacks
	bus       *EventBus
}

// NewBridgeService constructs a BridgeService from the given config, TLS certificate, and EventBus.
func NewBridgeService(cfg *config.Config, cert tls.Certificate, bus *EventBus) (*BridgeService, error) {
	eebusConfig, err := api.NewConfiguration(
		cfg.EEBUS.Vendor,
		cfg.EEBUS.Brand,
		cfg.EEBUS.Model,
		cfg.EEBUS.Serial,
		model.DeviceTypeTypeEnergyManagementSystem,
		[]model.EntityTypeType{model.EntityTypeTypeCEM},
		cfg.EEBUS.Port,
		cert,
		time.Second*4,
	)
	if err != nil {
		return nil, fmt.Errorf("creating eebus config: %w", err)
	}

	callbacks := NewCallbacks(bus)
	svc := eebusservice.NewService(eebusConfig, callbacks)

	return &BridgeService{
		service:   svc,
		callbacks: callbacks,
		bus:       bus,
	}, nil
}

// Setup initialises the underlying EEBUS service (mDNS, TLS, SHIP).
func (b *BridgeService) Setup() error {
	return b.service.Setup()
}

// Start begins accepting and initiating EEBUS connections.
func (b *BridgeService) Start() {
	b.service.Start()
}

// Shutdown gracefully stops the EEBUS service.
func (b *BridgeService) Shutdown() {
	b.service.Shutdown()
}

// Service returns the underlying eebus-go ServiceInterface.
func (b *BridgeService) Service() api.ServiceInterface {
	return b.service
}

// LocalEntity returns the local CEM entity used for registering use cases.
func (b *BridgeService) LocalEntity() spineapi.EntityLocalInterface {
	return b.service.LocalDevice().EntityForType(model.EntityTypeTypeCEM)
}

// Callbacks returns the callback handler (also the ServiceReaderInterface implementation).
func (b *BridgeService) Callbacks() *Callbacks {
	return b.callbacks
}

// RegisterRemoteSKI marks a SKI as trusted and initiates a connection.
func (b *BridgeService) RegisterRemoteSKI(ski string) {
	b.service.RegisterRemoteSKI(ski)
}

// UnregisterRemoteSKI removes trust for a SKI and disconnects if connected.
func (b *BridgeService) UnregisterRemoteSKI(ski string) {
	b.service.UnregisterRemoteSKI(ski)
}
