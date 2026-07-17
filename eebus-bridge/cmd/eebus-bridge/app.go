package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
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

type bridgeLifecycle interface {
	Start() error
	Shutdown()
	RegisterRemoteSKI(string)
	UnregisterRemoteSKI(string)
}

type grpcLifecycle interface {
	Start() error
	Stop()
	SetHealthy(bool)
}

type heartbeatLifecycle interface {
	StartHeartbeat(string) error
	StopHeartbeat() error
}

type monitoringRegistry interface {
	StaleDevices(time.Duration, time.Duration) []string
	MonitoringLastSuccessAge(string) (time.Duration, bool)
	MonitoringSuccessSince(string, time.Time) bool
	ClearEntities(string)
}

type deviceRecoveryStatus string

const (
	deviceRecoveryHealthy    deviceRecoveryStatus = "healthy"
	deviceRecoveryStale      deviceRecoveryStatus = "stale"
	deviceRecoveryInvalidate deviceRecoveryStatus = "invalidate"
	deviceRecoveryReconnect  deviceRecoveryStatus = "reconnect"
	deviceRecoveryRecovering deviceRecoveryStatus = "recovering"
	deviceRecoveryFailed     deviceRecoveryStatus = "failed"
)

type deviceRecoveryState struct {
	status           deviceRecoveryStatus
	attempts         int
	firstStaleAt     time.Time
	lastAttemptAt    time.Time
	recoverAfter     time.Time
	restartRequested bool
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

func useCaseRegistrar(bridgeSvc *eebus.BridgeService, modules ...eebusUseCaseRegistration) func() ([]string, error) {
	return func() ([]string, error) {
		names := make([]string, 0, len(modules))
		for _, module := range modules {
			if err := bridgeSvc.Service().AddUseCase(module.useCase); err != nil {
				return nil, fmt.Errorf("adding %s use case: %w", module.name, err)
			}
			names = append(names, module.name)
		}
		return names, nil
	}
}

type eebusUseCaseRegistration struct {
	name    string
	useCase eebusapi.UseCaseInterface
}

func newOHPCFModule(
	bridgeSvc *eebus.BridgeService,
	localEntity spineapi.EntityLocalInterface,
	ohpcfWrapper *usecases.OHPCFWrapper,
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
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
				eebusUseCaseRegistration{name: "OHPCF", useCase: ohpcfWrapper.UseCase()},
			)()
		},
		registerGRPC: func(srv *bridgegrpc.Server) {
			pb.RegisterOHPCFServiceServer(srv.GRPCServer(), bridgegrpc.NewOHPCFService(ohpcfWrapper, bus, registry))
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
	runtimeMu                  sync.Mutex
	cancelRuntime              context.CancelFunc
	recoveryMu                 sync.Mutex
	deviceRecoveries           map[string]*deviceRecoveryState
}

func run(ctx context.Context, cfg *config.Config) error {
	log.Printf("EEBUS debug_events=%t", cfg.Logging.DebugEvents)
	app, err := NewApplication(cfg)
	if err != nil {
		return err
	}
	return app.Start(ctx)
}

// NewApplication constructs and wires the complete daemon without starting its
// EEBUS or gRPC serve loops.
func NewApplication(cfg *config.Config) (_ *Application, retErr error) {
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

	if err := bridgeSvc.Setup(); err != nil {
		return nil, fmt.Errorf("setting up EEBUS service: %w", err)
	}

	app := &Application{
		cfg:                        cfg,
		bridgeSvc:                  bridgeSvc,
		registry:                   registry,
		monitoringWatchdogInterval: watchdogInterval,
		backgroundFailures:         make(chan backgroundFailure, 1),
	}
	defer func() {
		if retErr != nil {
			app.Stop()
		}
	}()

	localEntity := bridgeSvc.LocalEntity()
	if localEntity == nil {
		return nil, errors.New("local CEM entity is not available")
	}
	dhwTemperature := usecases.NewDHWTemperature(localEntity, bus, registry, cfg.Logging.DebugEvents)
	dhwSystemFunction := usecases.NewDHWSystemFunction(localEntity, bus, registry, cfg.Logging.DebugEvents)
	roomHeatingTemperature := usecases.NewRoomHeatingTemperature(localEntity, bus, registry, cfg.Logging.DebugEvents)
	roomHeatingSystemFunction := usecases.NewRoomHeatingSystemFunction(localEntity, bus, registry, cfg.Logging.DebugEvents)
	hydraulicTemperatures := usecases.NewHydraulicTemperatures(bus, registry, cfg.Logging.DebugEvents)
	deviceOperatingState := usecases.NewDeviceOperatingState(bus, registry, cfg.Logging.DebugEvents)

	mgcpProvider, err := setupMGCPProvider(cfg, bridgeSvc, bus)
	if err != nil {
		return nil, err
	}

	vapdProvider, err := setupVAPDProvider(cfg, bridgeSvc, bus)
	if err != nil {
		return nil, err
	}

	vabdProvider, err := setupVABDProvider(cfg, bridgeSvc, bus)
	if err != nil {
		return nil, err
	}

	// OHPCF (heat-pump compressor flexibility) CEM client. On by default; reads
	// the remote heat pump's optional-consumption offer and drives
	// schedule/pause/resume/abort via OHPCFService.
	var ohpcfWrapper *usecases.OHPCFWrapper
	if *cfg.OHPCF.Enabled {
		ohpcfWrapper = usecases.NewOHPCFWrapper(bus, registry, cfg.Logging.DebugEvents)
	}
	trustController := eebus.NewTrustController(bridgeSvc, registry, bus)

	modules := []applicationModule{
		{
			name: "Device",
			registerGRPC: func(srv *bridgegrpc.Server) {
				pb.RegisterDeviceServiceServer(
					srv.GRPCServer(),
					bridgegrpc.NewDeviceService(bridgeSvc.Callbacks(), bus, ski, registry, trustController),
				)
			},
		},
		{
			name: "LPC",
			setup: func() error {
				lpcWrapper.Setup(localEntity)
				return nil
			},
			registerUseCases: useCaseRegistrar(bridgeSvc, eebusUseCaseRegistration{name: "LPC", useCase: lpcWrapper.UseCase()}),
			registerGRPC: func(srv *bridgegrpc.Server) {
				pb.RegisterLPCServiceServer(srv.GRPCServer(), bridgegrpc.NewLPCService(lpcWrapper, bus, registry))
			},
			start: func() error {
				// Controllable systems revert an active LPC limit to its failsafe value when
				// heartbeats stop arriving, so keep the local heartbeat running for the
				// lifetime of the bridge.
				if err := lpcWrapper.StartHeartbeat(""); err != nil {
					log.Printf("starting LPC heartbeat failed: %v", err)
				} else {
					log.Println("Started LPC heartbeat")
				}
				return nil
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
				eebusUseCaseRegistration{name: "Monitoring", useCase: monitoringWrapper.UseCase()},
				eebusUseCaseRegistration{name: "DHWMonitoring", useCase: dhwMonitoringWrapper.UseCase()},
				eebusUseCaseRegistration{name: "MRT", useCase: roomMonitoringWrapper.UseCase()},
				eebusUseCaseRegistration{name: "MOT", useCase: outdoorMonitoringWrapper.UseCase()},
			),
			registerGRPC: func(srv *bridgegrpc.Server) {
				pb.RegisterMonitoringServiceServer(
					srv.GRPCServer(),
					bridgegrpc.NewMonitoringService(
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
					),
				)
			},
		},
		{
			name: "DHW",
			registerUseCases: useCaseRegistrar(
				bridgeSvc,
				eebusUseCaseRegistration{name: "DHWTemperature", useCase: dhwTemperature.UseCase()},
				eebusUseCaseRegistration{name: "DHWSystemFunction", useCase: dhwSystemFunction.UseCase()},
			),
			registerGRPC: func(srv *bridgegrpc.Server) {
				pb.RegisterDHWServiceServer(
					srv.GRPCServer(),
					bridgegrpc.NewDHWService(dhwTemperature, dhwSystemFunction, bus, registry),
				)
			},
		},
		{
			name: "HVAC",
			registerUseCases: useCaseRegistrar(
				bridgeSvc,
				eebusUseCaseRegistration{name: "RoomHeatingTemperature", useCase: roomHeatingTemperature.UseCase()},
				eebusUseCaseRegistration{name: "RoomHeatingSystemFunction", useCase: roomHeatingSystemFunction.UseCase()},
			),
			registerGRPC: func(srv *bridgegrpc.Server) {
				pb.RegisterHVACServiceServer(
					srv.GRPCServer(),
					bridgegrpc.NewHVACService(
						roomHeatingTemperature,
						roomHeatingSystemFunction,
						roomMonitoringWrapper,
						bus,
						registry,
					),
				)
			},
		},
	}
	modules = append(modules, newOHPCFModule(bridgeSvc, localEntity, ohpcfWrapper, bus, registry))
	modules = append(modules, applicationModule{
		name: "Providers",
		registerUseCases: func() ([]string, error) {
			registrations := make([]eebusUseCaseRegistration, 0, 3)
			if mgcpProvider != nil {
				registrations = append(registrations, eebusUseCaseRegistration{name: "MGCP", useCase: mgcpProvider.UseCase()})
			}
			if vapdProvider != nil {
				registrations = append(registrations, eebusUseCaseRegistration{name: "VAPD", useCase: vapdProvider.UseCase()})
			}
			if vabdProvider != nil {
				registrations = append(registrations, eebusUseCaseRegistration{name: "VABD", useCase: vabdProvider.UseCase()})
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
	})
	app.modules = modules

	registeredUseCases := make([]string, 0, len(modules)+4)
	for _, module := range app.modules {
		if module.setup != nil {
			if err := module.setup(); err != nil {
				return nil, fmt.Errorf("setting up %s module: %w", module.name, err)
			}
		}
		if module.registerUseCases != nil {
			names, err := module.registerUseCases()
			if err != nil {
				return nil, err
			}
			registeredUseCases = append(registeredUseCases, names...)
		}
		if module.start != nil {
			if err := module.start(); err != nil {
				return nil, fmt.Errorf("starting %s module: %w", module.name, err)
			}
		}
	}
	if ohpcfWrapper != nil {
		log.Println("[OHPCF] CEM client registered; awaiting remote compressor SmartEnergyManagementPs")
	}
	log.Printf("Registered EEBUS use cases: %s", strings.Join(registeredUseCases, ", "))

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

	registeredGRPCServices := make([]string, 0, len(app.modules))
	for _, module := range app.modules {
		if module.registerGRPC == nil {
			continue
		}
		module.registerGRPC(grpcSrv)
		registeredGRPCServices = append(registeredGRPCServices, module.name)
	}
	log.Printf("Registered gRPC services: %s", strings.Join(registeredGRPCServices, ", "))

	return app, nil
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
	if a.backgroundFailures == nil {
		a.backgroundFailures = make(chan backgroundFailure, 1)
	}

	runtimeCtx, cancel := context.WithCancel(ctx)
	a.runtimeMu.Lock()
	a.cancelRuntime = cancel
	a.runtimeMu.Unlock()
	defer a.Stop()

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

	if err := a.bridgeSvc.Start(); err != nil {
		return fmt.Errorf("EEBUS service start: %w", err)
	}
	log.Println("EEBUS bridge started")

	// SPIKE: trust a known remote SKI at startup so a test container can complete
	// the SHIP handshake without Home Assistant sending device.register_ski.
	if a.cfg.Experimental.TrustSKI != "" {
		a.bridgeSvc.RegisterRemoteSKI(a.cfg.Experimental.TrustSKI)
		log.Printf("[EXP] auto-trusted remote SKI: %s", a.cfg.Experimental.TrustSKI)
	}
	if a.cfg.Logging.DebugEvents {
		log.Println("[DEBUG] EEBUS event debug logging enabled; waiting for incoming callbacks")
	}

	a.startWatchdog(runtimeCtx)

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

func (a *Application) startWatchdog(ctx context.Context) {
	if a.registry == nil {
		return
	}
	interval := a.monitoringWatchdogInterval
	if interval <= 0 {
		interval = watchdogInterval
	}
	go func() {
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
}

func (a *Application) handleMonitoringWatchdogTick(now time.Time) bool {
	if a.registry == nil || a.grpcSrv == nil {
		return false
	}
	staleDevices := a.registry.StaleDevices(monitoringStaleThreshold, monitoringGracePeriod)
	staleSet := make(map[string]struct{}, len(staleDevices))
	for _, ski := range staleDevices {
		staleSet[eebus.NormalizeSKI(ski)] = struct{}{}
	}

	a.recoveryMu.Lock()
	defer a.recoveryMu.Unlock()

	a.grpcSrv.SetHealthy(true)
	for _, ski := range staleDevices {
		if a.recoverStaleDeviceLocked(now, ski) {
			a.grpcSrv.SetHealthy(false)
			return true
		}
	}
	for ski, recovery := range a.deviceRecoveries {
		if _, stale := staleSet[ski]; stale {
			continue
		}
		if a.recoverOutstandingDeviceLocked(now, ski, recovery) {
			a.grpcSrv.SetHealthy(false)
			return true
		}
	}
	return false
}

func (a *Application) recoverOutstandingDeviceLocked(now time.Time, ski string, recovery *deviceRecoveryState) bool {
	if recovery.status != deviceRecoveryRecovering {
		return false
	}
	if a.registry.MonitoringSuccessSince(ski, recovery.lastAttemptAt) {
		a.logRecoveryEvent(ski, deviceRecoveryHealthy, recovery.attempts, now.Sub(recovery.lastAttemptAt))
		delete(a.deviceRecoveries, ski)
		return false
	}
	if now.Before(recovery.recoverAfter) {
		return false
	}
	return a.recoverStaleDeviceLocked(now, ski)
}

func (a *Application) recoverStaleDeviceLocked(now time.Time, ski string) bool {
	if a.deviceRecoveries == nil {
		a.deviceRecoveries = make(map[string]*deviceRecoveryState)
	}
	ski = eebus.NormalizeSKI(ski)
	recovery := a.deviceRecoveries[ski]
	if recovery == nil {
		recovery = &deviceRecoveryState{status: deviceRecoveryHealthy}
		a.deviceRecoveries[ski] = recovery
	}
	if recovery.firstStaleAt.IsZero() {
		recovery.firstStaleAt = now
	}

	switch recovery.status {
	case deviceRecoveryRecovering:
		if now.Before(recovery.recoverAfter) {
			return false
		}
	case deviceRecoveryFailed:
		return false
	}

	if recovery.attempts >= monitoringRecoveryMaxAttempts {
		return a.failStaleDeviceLocked(now, ski, recovery)
	}

	attempt := recovery.attempts + 1
	recovery.status = deviceRecoveryStale
	a.logRecoveryEvent(ski, deviceRecoveryStale, attempt, now.Sub(recovery.firstStaleAt))

	a.registry.ClearEntities(ski)
	a.logRecoveryEvent(ski, deviceRecoveryInvalidate, attempt, 0)

	if a.bridgeSvc != nil {
		a.bridgeSvc.UnregisterRemoteSKI(ski)
		a.bridgeSvc.RegisterRemoteSKI(ski)
	}
	a.logRecoveryEvent(ski, deviceRecoveryReconnect, attempt, 0)

	recovery.status = deviceRecoveryRecovering
	recovery.attempts = attempt
	recovery.lastAttemptAt = now
	recovery.recoverAfter = now.Add(monitoringGracePeriod)
	a.logRecoveryEvent(ski, deviceRecoveryRecovering, attempt, monitoringGracePeriod)
	return false
}

func (a *Application) failStaleDeviceLocked(now time.Time, ski string, recovery *deviceRecoveryState) bool {
	recovery.status = deviceRecoveryFailed
	if recovery.restartRequested {
		return false
	}
	recovery.restartRequested = true
	a.logRecoveryEvent(ski, deviceRecoveryFailed, recovery.attempts, now.Sub(recovery.firstStaleAt))

	lastSuccessAge := "never"
	if age, ok := a.registry.MonitoringLastSuccessAge(ski); ok {
		lastSuccessAge = age.Round(time.Second).String()
	}
	reason := "monitoring watchdog recovery exhausted"
	log.Printf(
		"%s: devices=[%s(last_success_age=%s)] threshold=%s grace_period=%s attempts=%d; requesting restart",
		reason,
		eebus.ShortSKI(ski),
		lastSuccessAge,
		monitoringStaleThreshold,
		monitoringGracePeriod,
		recovery.attempts,
	)
	a.reportBackgroundFailure(reason, errors.New(reason))
	return true
}

func (a *Application) logRecoveryEvent(ski string, stage deviceRecoveryStatus, attempt int, duration time.Duration) {
	log.Printf(
		"monitoring recovery: ski=%s stage=%s attempt=%d duration=%s",
		eebus.ShortSKI(ski),
		stage,
		attempt,
		duration.Round(time.Second),
	)
}

func (a *Application) reportBackgroundFailure(reason string, err error) {
	select {
	case a.backgroundFailures <- backgroundFailure{reason: reason, err: err}:
	default:
	}
}

// Stop is safe to call repeatedly. Its component shutdown order deliberately
// remains heartbeat, gRPC, then EEBUS.
func (a *Application) Stop() {
	a.stopOnce.Do(func() {
		a.runtimeMu.Lock()
		cancel := a.cancelRuntime
		a.runtimeMu.Unlock()
		if cancel != nil {
			cancel()
		}

		log.Println("Shutting down...")
		for index := len(a.modules) - 1; index >= 0; index-- {
			module := a.modules[index]
			if module.stop == nil {
				continue
			}
			if err := module.stop(); err != nil {
				log.Printf("stopping %s module failed: %v", module.name, err)
			}
		}
		if a.grpcSrv != nil {
			a.grpcSrv.Stop()
		}
		if a.bridgeSvc != nil {
			a.bridgeSvc.Shutdown()
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
