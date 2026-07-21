package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"sync"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	spineapi "github.com/enbility/spine-go/api"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/certs"
	"github.com/volschin/eebus-bridge/internal/config"
	"github.com/volschin/eebus-bridge/internal/eebus"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// monitoringStaleThreshold bounds how long a connected device may go without a
// successful Monitoring entity resolution before the watchdog starts targeted
// recovery. The device normally pushes updates ~every 60s, so 10min gives
// ample margin against brief legitimate gaps.
const monitoringStaleThreshold = 10 * time.Minute

// monitoringGracePeriod allows a newly connected device to establish its
// first monitoring binding before watchdog staleness applies.
const monitoringGracePeriod = 2 * time.Minute

const watchdogInterval = 30 * time.Second
const monitoringRecoveryMaxAttempts = 3
const applicationShutdownTimeout = 5 * time.Second

type bridgeLifecycle interface {
	Setup() error
	Start() error
	Shutdown()
	RegisterRemoteSKI(string)
	UnregisterRemoteSKI(string)
}

type grpcLifecycle interface {
	Start() error
	WaitReady(context.Context) error
	Stop()
	SetHealthy(bool)
	SetDeviceHealthy(bool)
}

type heartbeatLifecycle interface {
	StartHeartbeat(string) error
	StopHeartbeat() error
}

type monitoringRegistry interface {
	eebus.RecoveryRegistry
}

type backgroundFailure struct {
	reason string
	err    error
}

type applicationModule struct {
	name             string
	setup            func() error
	registerUseCases func() ([]string, error)
	registerGRPC     func(*bridgegrpc.Server)
	start            func() error
	stop             func() error
}

type applicationProviderDiagnostics struct {
	mgcp *usecases.MGCPProvider
	vapd *usecases.VAPDProvider
	vabd *usecases.VABDProvider
}

func (p applicationProviderDiagnostics) ProviderDiagnostics(now time.Time) []*pb.ProviderSampleDiagnostics {
	result := make([]*pb.ProviderSampleDiagnostics, 0, 3)
	appendStatus := func(name string, status usecases.ProviderSnapshotDiagnostics) {
		entry := &pb.ProviderSampleDiagnostics{Provider: name, State: providerSampleState(status.State)}
		if !status.ObservedAt.IsZero() {
			entry.ObservedAt = timestamppb.New(status.ObservedAt)
		}
		if !status.ValidUntil.IsZero() {
			entry.ValidUntil = timestamppb.New(status.ValidUntil)
		}
		result = append(result, entry)
	}
	if p.mgcp != nil {
		appendStatus("grid", p.mgcp.Diagnostics(now))
	}
	if p.vapd != nil {
		appendStatus("pv", p.vapd.Diagnostics(now))
	}
	if p.vabd != nil {
		appendStatus("battery", p.vabd.Diagnostics(now))
	}
	return result
}

func providerSampleState(state usecases.ProviderSnapshotState) pb.ProviderSampleState {
	switch state {
	case usecases.ProviderSnapshotEmpty:
		return pb.ProviderSampleState_PROVIDER_SAMPLE_STATE_EMPTY
	case usecases.ProviderSnapshotCurrent:
		return pb.ProviderSampleState_PROVIDER_SAMPLE_STATE_CURRENT
	case usecases.ProviderSnapshotExpired:
		return pb.ProviderSampleState_PROVIDER_SAMPLE_STATE_EXPIRED
	case usecases.ProviderSnapshotClosed:
		return pb.ProviderSampleState_PROVIDER_SAMPLE_STATE_CLOSED
	default:
		return pb.ProviderSampleState_PROVIDER_SAMPLE_STATE_UNSPECIFIED
	}
}

func useCaseRegistrar(bridgeSvc *eebus.BridgeService, modules ...eebusUseCaseRegistration) func() ([]string, error) {
	return func() ([]string, error) {
		names := make([]string, 0, len(modules))
		for _, module := range modules {
			useCase, err := module.resolve()
			if err != nil {
				return nil, err
			}
			if err := bridgeSvc.Service().AddUseCase(useCase); err != nil {
				return nil, fmt.Errorf("adding %s use case: %w", module.name, err)
			}
			names = append(names, module.name)
		}
		return names, nil
	}
}

// eebusUseCaseRegistration resolves its use case lazily: the modules slice is
// built before the per-module setup() calls run, so capturing UseCase() eagerly
// in the slice literal would register the pre-Setup nil use case (startup
// panic in eebus-go AddFeatures).
type eebusUseCaseRegistration struct {
	name    string
	useCase func() eebusapi.UseCaseInterface
}

func (r eebusUseCaseRegistration) resolve() (eebusapi.UseCaseInterface, error) {
	if r.useCase == nil {
		return nil, fmt.Errorf("%s use case has no resolver", r.name)
	}
	useCase := r.useCase()
	if useCase == nil || (reflect.ValueOf(useCase).Kind() == reflect.Pointer && reflect.ValueOf(useCase).IsNil()) {
		return nil, fmt.Errorf("%s use case is not initialised", r.name)
	}
	return useCase, nil
}

func newOHPCFModule(
	bridgeSvc *eebus.BridgeService,
	localEntity spineapi.EntityLocalInterface,
	ohpcfWrapper *usecases.OHPCFWrapper,
	ohpcfService *bridgegrpc.OHPCFService,
) applicationModule {
	return applicationModule{
		name: "OHPCF",
		setup: func() error {
			if ohpcfWrapper != nil {
				ohpcfWrapper.Setup(localEntity)
			}
			return nil
		},
		registerUseCases: func() ([]string, error) {
			if ohpcfWrapper == nil {
				return nil, nil
			}
			return useCaseRegistrar(
				bridgeSvc,
				eebusUseCaseRegistration{name: "OHPCF", useCase: func() eebusapi.UseCaseInterface { return ohpcfWrapper.UseCase() }},
			)()
		},
		registerGRPC: func(srv *bridgegrpc.Server) {
			pb.RegisterOHPCFServiceServer(srv.GRPCServer(), ohpcfService)
		},
	}
}

// controlledShutdownError marks a runtime failure that Start already logged
// before performing the controlled shutdown sequence.
type controlledShutdownError struct {
	err error
}

func (e *controlledShutdownError) Error() string { return e.err.Error() }
func (e *controlledShutdownError) Unwrap() error { return e.err }

type signalShutdown struct {
	signal os.Signal
}

func (s *signalShutdown) Error() string { return "received signal " + s.signal.String() }

// Application owns the daemon's EEBUS, use-case, gRPC, watchdog, and shutdown
// lifecycle. The small lifecycle interfaces keep failure fan-out and teardown
// directly testable without constructing a real EEBUS stack or TCP listener.
type Application struct {
	cfg *config.Config

	bridgeSvc bridgeLifecycle
	grpcSrv   grpcLifecycle
	registry  monitoringRegistry

	monitoringWatchdogInterval time.Duration
	modules                    []applicationModule
	backgroundFailures         chan backgroundFailure
	stopOnce                   sync.Once
	lifecycleMu                sync.Mutex
	lifecycle                  *lifecycleTransaction
	stopped                    bool
	startupMu                  sync.Mutex
	cancelRuntime              context.CancelFunc
	watchdogWG                 sync.WaitGroup
	recoverySupervisor         *eebus.RecoverySupervisor
	prepareMu                  sync.Mutex
	prepared                   bool
	registeredUseCases         []string
	compose                    func() error
}

func run(ctx context.Context, cfg *config.Config) error {
	log.Printf("EEBUS debug_events=%t", cfg.Logging.DebugEvents)
	app, err := NewApplication(cfg)
	if err != nil {
		return err
	}
	return app.Start(ctx)
}

// NewApplication constructs the daemon's inert core dependencies. EEBUS setup,
// local-entity-dependent composition, handler/use-case registration, and every
// long-running component are deferred to Start.
func NewApplication(cfg *config.Config) (*Application, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}

	cert, err := certs.EnsureCertificate(
		cfg.Certificates.CertFile,
		cfg.Certificates.KeyFile,
		cfg.Certificates.StoragePath,
		cfg.Certificates.AutoGenerate != nil && *cfg.Certificates.AutoGenerate,
	)
	if err != nil {
		return nil, fmt.Errorf("certificate: %w", err)
	}

	ski, err := certs.SKIFromCertificate(cert)
	if err != nil {
		return nil, fmt.Errorf("extracting SKI: %w", err)
	}
	log.Printf("Local SKI: %s", ski)

	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()
	registry.SetLocalCapabilityEnabled(eebus.CapabilityOHPCF, *cfg.OHPCF.Enabled)

	bridgeSvc, err := eebus.NewBridgeService(cfg, cert, bus)
	if err != nil {
		return nil, fmt.Errorf("creating bridge service: %w", err)
	}

	// Let disconnect callbacks drop cached entity refs (set before service start,
	// so no remote callback races the assignment).
	bridgeSvc.Callbacks().SetRegistry(registry)

	lpcWrapper := usecases.NewLPCWrapper(bus, registry, cfg.Logging.DebugEvents)
	monitoringWrapper := usecases.NewMonitoringWrapper(bus, registry, cfg.Logging.DebugEvents)
	dhwMonitoringWrapper := usecases.NewDHWMonitoringWrapper(bus, registry, cfg.Logging.DebugEvents)
	roomMonitoringWrapper := usecases.NewRoomMonitoringWrapper(bus, registry, cfg.Logging.DebugEvents)
	outdoorMonitoringWrapper := usecases.NewOutdoorMonitoringWrapper(bus, registry, cfg.Logging.DebugEvents)

	app := &Application{
		cfg:                        cfg,
		bridgeSvc:                  bridgeSvc,
		registry:                   registry,
		monitoringWatchdogInterval: watchdogInterval,
		backgroundFailures:         make(chan backgroundFailure, 1),
	}
	app.recoverySupervisor = eebus.NewRecoverySupervisor(registry, bridgeSvc, eebus.RecoveryConfig{
		StaleThreshold: monitoringStaleThreshold,
		GracePeriod:    monitoringGracePeriod,
		BaseBackoff:    monitoringGracePeriod,
		MaxBackoff:     10 * time.Minute,
		MaxAttempts:    monitoringRecoveryMaxAttempts,
	})
	grpcSrv, err := bridgegrpc.NewServerWithSecurity(
		cfg.GRPC.Bind,
		cfg.GRPC.Port,
		cfg.GRPC.EnableReflection,
		cfg.GRPC.Security,
	)
	if err != nil {
		return nil, fmt.Errorf("configuring gRPC server security: %w", err)
	}
	app.grpcSrv = grpcSrv
	app.compose = func() error {
		localEntity := bridgeSvc.LocalEntity()
		if localEntity == nil {
			return errors.New("local CEM entity is not available")
		}
		dhwTemperature := usecases.NewDHWTemperature(localEntity, bus, registry, cfg.Logging.DebugEvents)
		dhwSystemFunctionMonitoring := usecases.NewDHWSystemFunctionMonitoring(bus, registry, cfg.Logging.DebugEvents)
		// Keep the proven CDSF implementation for writes, but let MDSF own
		// capability discovery, reads, and events.
		dhwSystemFunctionUseCase := usecases.NewDHWSystemFunction(localEntity, nil, nil, cfg.Logging.DebugEvents)
		dhwSystemFunctionConfiguration := usecases.NewLegacyDHWSystemFunctionConfiguration(dhwSystemFunctionUseCase)
		dhwSystemFunction := usecases.NewDHWSystemFunctionAdapter(
			dhwSystemFunctionMonitoring,
			dhwSystemFunctionConfiguration,
		)
		roomHeatingTemperature := usecases.NewRoomHeatingTemperature(localEntity, bus, registry, cfg.Logging.DebugEvents)
		roomHeatingSystemFunction := usecases.NewRoomHeatingSystemFunction(localEntity, bus, registry, cfg.Logging.DebugEvents)
		hydraulicTemperatures := usecases.NewHydraulicTemperatures(bus, registry, cfg.Logging.DebugEvents)
		deviceOperatingState := usecases.NewDeviceOperatingState(bus, registry, cfg.Logging.DebugEvents)

		mgcpProvider, err := setupMGCPProvider(cfg, bridgeSvc, bus)
		if err != nil {
			return err
		}

		vapdProvider, err := setupVAPDProvider(cfg, bridgeSvc, bus)
		if err != nil {
			return err
		}

		vabdProvider, err := setupVABDProvider(cfg, bridgeSvc, bus)
		if err != nil {
			return err
		}
		providerDiagnostics := applicationProviderDiagnostics{
			mgcp: mgcpProvider, vapd: vapdProvider, vabd: vabdProvider,
		}

		// OHPCF (heat-pump compressor flexibility) CEM client. On by default; reads
		// the remote heat pump's optional-consumption offer and drives
		// schedule/pause/resume/abort via OHPCFService.
		var ohpcfWrapper *usecases.OHPCFWrapper
		if *cfg.OHPCF.Enabled {
			ohpcfWrapper = usecases.NewOHPCFWrapper(bus, registry, cfg.Logging.DebugEvents)
		}
		trustController := eebus.NewTrustController(bridgeSvc, registry, bus)

		lpcService := bridgegrpc.NewLPCService(lpcWrapper, bus, registry)
		monitoringService := bridgegrpc.NewMonitoringService(
			monitoringWrapper,
			bridgegrpc.MonitoringReaders{
				DHW:         dhwMonitoringWrapper,
				Room:        roomMonitoringWrapper,
				Outdoor:     outdoorMonitoringWrapper,
				Flow:        usecases.FlowTemperatureReader{HydraulicTemperatures: hydraulicTemperatures},
				Return:      usecases.ReturnTemperatureReader{HydraulicTemperatures: hydraulicTemperatures},
				Diagnostics: deviceOperatingState,
			},
			bus,
			registry,
			cfg.Logging.DebugEvents,
		)
		dhwService := bridgegrpc.NewDHWService(dhwTemperature, dhwSystemFunction, bus, registry)
		hvacService := bridgegrpc.NewHVACService(
			roomHeatingTemperature,
			roomHeatingSystemFunction,
			roomMonitoringWrapper,
			bus,
			registry,
		)
		ohpcfService := bridgegrpc.NewOHPCFService(ohpcfWrapper, bus, registry)

		modules := []applicationModule{
			{
				name: "Device",
				registerGRPC: func(srv *bridgegrpc.Server) {
					pb.RegisterDeviceServiceServer(
						srv.GRPCServer(),
						bridgegrpc.NewDeviceService(
							bridgeSvc.Callbacks(),
							bus,
							ski,
							registry,
							trustController,
							bridgegrpc.WithDeviceStatePayloads(bridgegrpc.DeviceStatePayloadSources{
								Monitoring: monitoringService,
								LPC:        lpcService,
								DHW:        dhwService,
								HVAC:       hvacService,
								OHPCF:      ohpcfService,
							}),
							bridgegrpc.WithOperationalDiagnostics(app.recoverySupervisor, providerDiagnostics),
						),
					)
				},
			},
			{
				name: "LPC",
				setup: func() error {
					lpcWrapper.Setup(localEntity)
					return nil
				},
				registerUseCases: useCaseRegistrar(bridgeSvc, eebusUseCaseRegistration{
					name:    "LPC",
					useCase: func() eebusapi.UseCaseInterface { return lpcWrapper.UseCase() },
				}),
				registerGRPC: func(srv *bridgegrpc.Server) {
					pb.RegisterLPCServiceServer(srv.GRPCServer(), lpcService)
				},
				start: func() error {
					// Controllable systems revert an active LPC limit to its failsafe value when
					// heartbeats stop arriving, so keep the local heartbeat running for the
					// lifetime of the bridge.
					return startLPCHeartbeat(lpcWrapper)
				},
				stop: lpcWrapper.StopHeartbeat,
			},
			{
				name: "Monitoring",
				setup: func() error {
					monitoringWrapper.Setup(localEntity)
					dhwMonitoringWrapper.Setup(localEntity)
					roomMonitoringWrapper.Setup(localEntity)
					outdoorMonitoringWrapper.Setup(localEntity)
					hydraulicTemperatures.Setup(localEntity)
					deviceOperatingState.Setup(localEntity)
					return nil
				},
				registerUseCases: useCaseRegistrar(
					bridgeSvc,
					eebusUseCaseRegistration{name: "Monitoring", useCase: func() eebusapi.UseCaseInterface { return monitoringWrapper.UseCase() }},
					eebusUseCaseRegistration{name: "DHWMonitoring", useCase: func() eebusapi.UseCaseInterface { return dhwMonitoringWrapper.UseCase() }},
					eebusUseCaseRegistration{name: "MRT", useCase: func() eebusapi.UseCaseInterface { return roomMonitoringWrapper.UseCase() }},
					eebusUseCaseRegistration{name: "MOT", useCase: func() eebusapi.UseCaseInterface { return outdoorMonitoringWrapper.UseCase() }},
				),
				registerGRPC: func(srv *bridgegrpc.Server) {
					pb.RegisterMonitoringServiceServer(srv.GRPCServer(), monitoringService)
				},
			},
			{
				name: "DHW",
				setup: func() error {
					dhwSystemFunctionMonitoring.Setup(localEntity)
					return nil
				},
				registerUseCases: useCaseRegistrar(
					bridgeSvc,
					eebusUseCaseRegistration{name: "DHWTemperature", useCase: func() eebusapi.UseCaseInterface { return dhwTemperature.UseCase() }},
					eebusUseCaseRegistration{name: "MDSF", useCase: func() eebusapi.UseCaseInterface { return dhwSystemFunctionMonitoring.UseCase() }},
					eebusUseCaseRegistration{name: "DHWSystemFunctionConfiguration", useCase: func() eebusapi.UseCaseInterface { return dhwSystemFunctionUseCase.UseCase() }},
				),
				registerGRPC: func(srv *bridgegrpc.Server) {
					pb.RegisterDHWServiceServer(srv.GRPCServer(), dhwService)
				},
			},
			{
				name: "HVAC",
				registerUseCases: useCaseRegistrar(
					bridgeSvc,
					eebusUseCaseRegistration{name: "RoomHeatingTemperature", useCase: func() eebusapi.UseCaseInterface { return roomHeatingTemperature.UseCase() }},
					eebusUseCaseRegistration{name: "RoomHeatingSystemFunction", useCase: func() eebusapi.UseCaseInterface { return roomHeatingSystemFunction.UseCase() }},
				),
				registerGRPC: func(srv *bridgegrpc.Server) {
					pb.RegisterHVACServiceServer(srv.GRPCServer(), hvacService)
				},
			},
		}
		modules = append(modules, newOHPCFModule(bridgeSvc, localEntity, ohpcfWrapper, ohpcfService))
		modules = append(modules, applicationModule{
			name:  "Providers",
			start: func() error { return nil },
			registerUseCases: func() ([]string, error) {
				registrations := make([]eebusUseCaseRegistration, 0, 3)
				if mgcpProvider != nil {
					registrations = append(registrations, eebusUseCaseRegistration{name: "MGCP", useCase: func() eebusapi.UseCaseInterface { return mgcpProvider.UseCase() }})
				}
				if vapdProvider != nil {
					registrations = append(registrations, eebusUseCaseRegistration{name: "VAPD", useCase: func() eebusapi.UseCaseInterface { return vapdProvider.UseCase() }})
				}
				if vabdProvider != nil {
					registrations = append(registrations, eebusUseCaseRegistration{name: "VABD", useCase: func() eebusapi.UseCaseInterface { return vabdProvider.UseCase() }})
				}
				if len(registrations) == 0 {
					return nil, nil
				}
				names, err := useCaseRegistrar(bridgeSvc, registrations...)()
				if err != nil {
					return nil, err
				}
				log.Println("[PROVIDERS] experimental provider use cases registered; awaiting data via gRPC push services")
				return names, nil
			},
			registerGRPC: func(srv *bridgegrpc.Server) {
				gridSvc := bridgegrpc.NewGridService(mgcpProvider)
				visualizationSvc := bridgegrpc.NewVisualizationService(vapdProvider, vabdProvider)
				if bridgegrpc.RegisterPushServices(srv, cfg.GRPC.Bind, cfg.GRPC.Security.Mode, gridSvc, visualizationSvc) {
					log.Println("Registered provider push services (grid/PV/battery)")
				} else {
					log.Printf("Refusing to register provider push services: gRPC bind %q is not secured", cfg.GRPC.Bind)
				}
			},
			stop: func() error {
				var closeErrors []error
				if mgcpProvider != nil {
					closeErrors = append(closeErrors, mgcpProvider.Close())
				}
				if vapdProvider != nil {
					closeErrors = append(closeErrors, vapdProvider.Close())
				}
				if vabdProvider != nil {
					closeErrors = append(closeErrors, vabdProvider.Close())
				}
				return errors.Join(closeErrors...)
			},
		})
		app.modules = modules

		registeredGRPCServices := make([]string, 0, len(app.modules))
		for _, module := range app.modules {
			if module.registerGRPC == nil {
				continue
			}
			module.registerGRPC(grpcSrv)
			registeredGRPCServices = append(registeredGRPCServices, module.name)
		}
		log.Printf("Registered gRPC services: %s", strings.Join(registeredGRPCServices, ", "))
		return nil
	}

	return app, nil
}

// prepareForStart completes local-entity-dependent composition after EEBUS
// Setup. No heartbeat, watchdog, listener, or provider timer is started here.
func (a *Application) prepareForStart() error {
	a.prepareMu.Lock()
	defer a.prepareMu.Unlock()
	if a.prepared {
		return nil
	}
	if a.compose != nil {
		if err := a.compose(); err != nil {
			return fmt.Errorf("composing application modules: %w", err)
		}
		a.compose = nil
	}

	registeredUseCases := make([]string, 0, len(a.modules)+4)
	for _, module := range a.modules {
		if module.setup != nil {
			if err := module.setup(); err != nil {
				return fmt.Errorf("setting up %s module: %w", module.name, err)
			}
		}
		if module.registerUseCases != nil {
			names, err := module.registerUseCases()
			if err != nil {
				return err
			}
			registeredUseCases = append(registeredUseCases, names...)
		}
	}
	log.Printf("Registered EEBUS use cases: %s", strings.Join(registeredUseCases, ", "))
	a.registeredUseCases = append([]string(nil), registeredUseCases...)
	a.prepared = true
	return nil
}

func setupMGCPProvider(cfg *config.Config, bridgeSvc *eebus.BridgeService, bus *eebus.EventBus) (*usecases.MGCPProvider, error) {
	if !cfg.Experimental.MGCPProvider {
		return nil, nil
	}
	gridEntity := bridgeSvc.GridEntity()
	if gridEntity == nil {
		log.Println("[MGCP] experimental provider enabled but grid entity is unavailable; skipping")
		return nil, nil
	}
	provider := usecases.NewMGCPProvider(gridEntity, bus, cfg.Logging.DebugEvents)
	log.Println("[MGCP] experimental grid-connection-point provider prepared")
	return provider, nil
}

func setupVAPDProvider(cfg *config.Config, bridgeSvc *eebus.BridgeService, bus *eebus.EventBus) (*usecases.VAPDProvider, error) {
	if !cfg.Experimental.VAPDProvider {
		return nil, nil
	}
	pvEntity := bridgeSvc.PVEntity()
	if pvEntity == nil {
		log.Println("[VAPD] experimental provider enabled but PV entity is unavailable; skipping")
		return nil, nil
	}
	provider := usecases.NewVAPDProvider(pvEntity, bus, cfg.Logging.DebugEvents)
	log.Println("[VAPD] experimental PV-system provider prepared")
	return provider, nil
}

func setupVABDProvider(cfg *config.Config, bridgeSvc *eebus.BridgeService, bus *eebus.EventBus) (*usecases.VABDProvider, error) {
	if !cfg.Experimental.VABDProvider {
		return nil, nil
	}
	batteryEntity := bridgeSvc.BatteryEntity()
	if batteryEntity == nil {
		log.Println("[VABD] experimental provider enabled but battery entity is unavailable; skipping")
		return nil, nil
	}
	provider := usecases.NewVABDProvider(batteryEntity, bus, cfg.Logging.DebugEvents)
	log.Println("[VABD] experimental battery-system provider prepared")
	return provider, nil
}

// Start launches the serve loop, starts EEBUS, and waits for the first signal
// or fatal background component failure. Every exit path runs Stop.
func (a *Application) Start(ctx context.Context) error {
	if a.bridgeSvc == nil {
		return errors.New("EEBUS bridge service is not configured")
	}
	if a.grpcSrv == nil {
		return errors.New("gRPC server is not configured")
	}
	tx := newLifecycleTransaction()
	runtimeCtx, cancel := context.WithCancel(ctx)
	a.lifecycleMu.Lock()
	if a.stopped {
		a.lifecycleMu.Unlock()
		cancel()
		return errors.New("application is stopped")
	}
	if a.lifecycle != nil {
		a.lifecycleMu.Unlock()
		cancel()
		return errors.New("application is already started")
	}
	if a.backgroundFailures == nil {
		a.backgroundFailures = make(chan backgroundFailure, 1)
	}
	a.lifecycle = tx
	a.cancelRuntime = cancel
	a.lifecycleMu.Unlock()
	a.grpcSrv.SetHealthy(false)

	defer a.Stop()
	if err := a.startComponents(runtimeCtx, tx); err != nil {
		return err
	}

	select {
	case <-runtimeCtx.Done():
		var sig *signalShutdown
		if errors.As(context.Cause(ctx), &sig) {
			log.Printf("Received signal %s", sig.signal)
		}
		return nil
	case failure := <-a.backgroundFailures:
		log.Printf("Controlled shutdown requested: %s", failure.reason)
		return &controlledShutdownError{err: failure.err}
	}
}

func (a *Application) startComponents(runtimeCtx context.Context, tx *lifecycleTransaction) error {
	a.startupMu.Lock()
	defer a.startupMu.Unlock()
	if err := runtimeCtx.Err(); err != nil {
		return fmt.Errorf("application startup canceled: %w", err)
	}
	if err := a.bridgeSvc.Setup(); err != nil {
		return fmt.Errorf("setting up EEBUS service: %w", err)
	}
	if err := tx.add("EEBUS", func() error {
		a.bridgeSvc.Shutdown()
		return nil
	}); err != nil {
		return err
	}
	if err := a.prepareForStart(); err != nil {
		return err
	}
	if err := runtimeCtx.Err(); err != nil {
		return fmt.Errorf("application startup canceled after composition: %w", err)
	}
	if err := a.bridgeSvc.Start(); err != nil {
		return fmt.Errorf("EEBUS service start: %w", err)
	}
	log.Println("EEBUS bridge started")
	for _, module := range a.modules {
		if err := runtimeCtx.Err(); err != nil {
			return fmt.Errorf("application startup canceled before %s module: %w", module.name, err)
		}
		if module.start == nil {
			continue
		}
		if err := module.start(); err != nil {
			return fmt.Errorf("starting %s module: %w", module.name, err)
		}
		module := module
		if err := tx.add(module.name, module.stop); err != nil {
			return err
		}
	}

	go func() {
		log.Printf("gRPC server listening on %s:%d", a.cfg.GRPC.Bind, a.cfg.GRPC.Port)
		if err := a.grpcSrv.Start(); err != nil {
			// Stop makes Serve return an error as part of normal teardown. The
			// context path already owns that shutdown and must not be reclassified
			// as a competing runtime failure.
			select {
			case <-runtimeCtx.Done():
				return
			default:
			}
			wrapped := fmt.Errorf("gRPC server: %w", err)
			a.reportBackgroundFailure(wrapped.Error(), wrapped)
		}
	}()
	if err := a.grpcSrv.WaitReady(runtimeCtx); err != nil {
		a.grpcSrv.Stop()
		return fmt.Errorf("gRPC server readiness: %w", err)
	}
	if err := tx.add("gRPC", func() error {
		a.grpcSrv.Stop()
		return nil
	}); err != nil {
		return err
	}

	// SPIKE: trust a known remote SKI at startup so a test container can complete
	// the SHIP handshake without Home Assistant sending device.register_ski.
	if a.cfg.Experimental.TrustSKI != "" {
		a.bridgeSvc.RegisterRemoteSKI(a.cfg.Experimental.TrustSKI)
		log.Printf("[EXP] auto-trusted remote SKI: %s", a.cfg.Experimental.TrustSKI)
	}
	if a.cfg.Logging.DebugEvents {
		log.Println("[DEBUG] EEBUS event debug logging enabled; waiting for incoming callbacks")
	}

	if a.startWatchdog(runtimeCtx) {
		if err := tx.add("monitoring watchdog", a.waitForWatchdog); err != nil {
			return err
		}
	}
	a.lifecycleMu.Lock()
	if a.stopped || runtimeCtx.Err() != nil {
		a.lifecycleMu.Unlock()
		return fmt.Errorf("application startup canceled before serving: %w", context.Canceled)
	}
	// This is the atomic startup commit. Stop sets stopped and NOT_SERVING
	// under the same mutex, so SERVING can never be published after shutdown
	// has begun.
	a.grpcSrv.SetHealthy(true)
	a.lifecycleMu.Unlock()
	return nil
}

func startLPCHeartbeat(heartbeat heartbeatLifecycle) error {
	if err := heartbeat.StartHeartbeat(""); err != nil {
		log.Printf("Failed to start LPC heartbeat: %v", err)
		return nil
	}
	log.Println("Started LPC heartbeat")
	return nil
}

func (a *Application) startWatchdog(ctx context.Context) bool {
	if a.registry == nil {
		return false
	}
	interval := a.monitoringWatchdogInterval
	if interval <= 0 {
		interval = watchdogInterval
	}
	a.watchdogWG.Add(1)
	go func() {
		defer a.watchdogWG.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if a.handleMonitoringWatchdogTick(time.Now()) {
					return
				}
			}
		}
	}()
	return true
}

func (a *Application) waitForWatchdog() error {
	done := make(chan struct{})
	go func() {
		a.watchdogWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(applicationShutdownTimeout):
		return errors.New("timed out waiting for monitoring watchdog")
	}
}

func (a *Application) handleMonitoringWatchdogTick(now time.Time) bool {
	if a.recoverySupervisor == nil {
		return false
	}
	result := a.recoverySupervisor.Tick(now)
	if a.grpcSrv != nil && a.registry != nil {
		healthy := true
		for _, device := range a.registry.ListDeviceHealth() {
			if device.TrustKnown && !device.Trusted {
				continue
			}
			if !device.Connected || a.recoverySupervisor.Snapshot(device.SKI, now).State != eebus.RecoveryStateHealthy {
				healthy = false
				break
			}
		}
		a.grpcSrv.SetDeviceHealthy(healthy)
	}
	if !result.RestartRequired {
		return false
	}
	reason := "monitoring watchdog recovery exhausted"
	redacted := make([]string, 0, len(result.ExhaustedSKIs))
	for _, ski := range result.ExhaustedSKIs {
		redacted = append(redacted, eebus.ShortSKI(ski))
	}
	log.Printf("%s: devices=%v; requesting restart", reason, redacted)
	a.reportBackgroundFailure(reason, errors.New(reason))
	return true
}

func (a *Application) reportBackgroundFailure(reason string, err error) {
	select {
	case a.backgroundFailures <- backgroundFailure{reason: reason, err: err}:
	default:
	}
}

// Stop is safe to call repeatedly and concurrently. Only successfully started
// stages are rolled back, in exact reverse startup order.
func (a *Application) Stop() {
	a.stopOnce.Do(func() {
		a.lifecycleMu.Lock()
		cancel := a.cancelRuntime
		tx := a.lifecycle
		a.stopped = true
		if a.grpcSrv != nil {
			a.grpcSrv.SetHealthy(false)
		}
		a.lifecycleMu.Unlock()
		if cancel != nil {
			cancel()
		}

		log.Println("Shutting down...")
		a.startupMu.Lock()
		defer a.startupMu.Unlock()
		if tx != nil {
			if err := tx.rollback(); err != nil {
				log.Printf("shutdown completed with errors: %v", err)
			}
		}
		log.Println("Shutdown complete")
	})
}

func logRunError(err error) {
	var controlled *controlledShutdownError
	if !errors.As(err, &controlled) {
		log.Print(err)
	}
}

func notifySignalContext(parent context.Context, signals ...os.Signal) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, signals...)
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case sig := <-signalCh:
			cancel(&signalShutdown{signal: sig})
		case <-ctx.Done():
		}
	}()

	var stopOnce sync.Once
	return ctx, func() {
		stopOnce.Do(func() {
			signal.Stop(signalCh)
			cancel(context.Canceled)
		})
		<-done
	}
}
