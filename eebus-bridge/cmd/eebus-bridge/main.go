package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/certs"
	"github.com/volschin/eebus-bridge/internal/config"
	"github.com/volschin/eebus-bridge/internal/eebus"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"github.com/volschin/eebus-bridge/internal/usecases"
)

// monitoringStaleThreshold bounds how long a connected device may go without a
// successful Monitoring entity resolution before the watchdog restarts the
// process. Reconnects (SHIP re-pair) can leave the SPINE entity binding stuck
// with no error logged, silently starving Home Assistant of data; a restart
// forces a clean re-handshake. The device normally pushes updates ~every 60s,
// so 10min gives ample margin against brief legitimate gaps.
const monitoringStaleThreshold = 10 * time.Minute

// monitoringGracePeriod allows a newly connected device to establish its
// first monitoring binding before watchdog staleness applies.
const monitoringGracePeriod = 2 * time.Minute

const watchdogInterval = 30 * time.Second

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	healthcheck := flag.Bool("healthcheck", false, "probe the gRPC health service and exit")
	flag.Parse()

	if *healthcheck {
		if err := runHealthcheck(*configPath); err != nil {
			log.Fatalf("healthcheck: %v", err)
		}
		return
	}

	cfg, err := config.LoadFromFile(*configPath)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}
	log.Printf("EEBUS debug_events=%t", cfg.Logging.DebugEvents)

	cert, err := certs.EnsureCertificate(
		cfg.Certificates.CertFile,
		cfg.Certificates.KeyFile,
		cfg.Certificates.StoragePath,
	)
	if err != nil {
		log.Fatalf("certificate: %v", err)
	}

	ski, err := certs.SKIFromCertificate(cert)
	if err != nil {
		log.Fatalf("extracting SKI: %v", err)
	}
	log.Printf("Local SKI: %s", ski)

	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()

	bridgeSvc, err := eebus.NewBridgeService(cfg, cert, bus)
	if err != nil {
		log.Fatalf("creating bridge service: %v", err)
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
		log.Fatalf("setting up EEBUS service: %v", err)
	}

	localEntity := bridgeSvc.LocalEntity()
	if localEntity == nil {
		log.Fatal("local CEM entity is not available")
	}
	lpcWrapper.Setup(localEntity)
	monitoringWrapper.Setup(localEntity)
	dhwMonitoringWrapper.Setup(localEntity)
	roomMonitoringWrapper.Setup(localEntity)
	outdoorMonitoringWrapper.Setup(localEntity)
	dhwTemperature := usecases.NewDHWTemperature(localEntity, bus, registry, cfg.Logging.DebugEvents)
	dhwSystemFunction := usecases.NewDHWSystemFunction(localEntity, bus, registry, cfg.Logging.DebugEvents)
	roomHeatingTemperature := usecases.NewRoomHeatingTemperature(localEntity, bus, registry, cfg.Logging.DebugEvents)
	roomHeatingSystemFunction := usecases.NewRoomHeatingSystemFunction(localEntity, bus, registry, cfg.Logging.DebugEvents)
	hydraulicTemperatures := usecases.NewHydraulicTemperatures(bus, registry, cfg.Logging.DebugEvents)
	hydraulicTemperatures.Setup(localEntity)
	deviceOperatingState := usecases.NewDeviceOperatingState(bus, registry, cfg.Logging.DebugEvents)
	deviceOperatingState.Setup(localEntity)
	if err := bridgeSvc.Service().AddUseCase(lpcWrapper.UseCase()); err != nil {
		log.Fatalf("adding LPC use case: %v", err)
	}
	if err := bridgeSvc.Service().AddUseCase(monitoringWrapper.UseCase()); err != nil {
		log.Fatalf("adding monitoring use case: %v", err)
	}
	if err := bridgeSvc.Service().AddUseCase(dhwMonitoringWrapper.UseCase()); err != nil {
		log.Fatalf("adding DHW monitoring use case: %v", err)
	}
	if err := bridgeSvc.Service().AddUseCase(roomMonitoringWrapper.UseCase()); err != nil {
		log.Fatalf("adding room monitoring use case: %v", err)
	}
	if err := bridgeSvc.Service().AddUseCase(outdoorMonitoringWrapper.UseCase()); err != nil {
		log.Fatalf("adding outdoor monitoring use case: %v", err)
	}
	if err := bridgeSvc.Service().AddUseCase(dhwTemperature.UseCase()); err != nil {
		log.Fatalf("adding DHW temperature use case: %v", err)
	}
	if err := bridgeSvc.Service().AddUseCase(dhwSystemFunction.UseCase()); err != nil {
		log.Fatalf("adding DHW system function use case: %v", err)
	}
	if err := bridgeSvc.Service().AddUseCase(roomHeatingTemperature.UseCase()); err != nil {
		log.Fatalf("adding room heating temperature use case: %v", err)
	}
	if err := bridgeSvc.Service().AddUseCase(roomHeatingSystemFunction.UseCase()); err != nil {
		log.Fatalf("adding room heating system function use case: %v", err)
	}
	registeredUseCases := []string{
		"LPC", "Monitoring", "DHWMonitoring", "MRT", "MOT", "DHWTemperature", "DHWSystemFunction",
		"RoomHeatingTemperature", "RoomHeatingSystemFunction",
	}

	// SPIKE: experimental MGCP grid-connection-point provider. Off by default.
	var mgcpProvider *usecases.MGCPProvider
	if cfg.Experimental.MGCPProvider {
		gridEntity := bridgeSvc.GridEntity()
		if gridEntity == nil {
			log.Println("[MGCP] experimental provider enabled but grid entity is unavailable; skipping")
		} else {
			mgcpProvider = usecases.NewMGCPProvider(gridEntity, bus, cfg.Logging.DebugEvents)
			if err := bridgeSvc.Service().AddUseCase(mgcpProvider.UseCase()); err != nil {
				log.Fatalf("adding MGCP use case: %v", err)
			}
			log.Println("[MGCP] experimental grid-connection-point provider registered; awaiting grid data via GridService")
			registeredUseCases = append(registeredUseCases, "MGCP")
		}
	}

	// SPIKE: experimental VAPD (PV) display provider. Off by default.
	var vapdProvider *usecases.VAPDProvider
	if cfg.Experimental.VAPDProvider {
		pvEntity := bridgeSvc.PVEntity()
		if pvEntity == nil {
			log.Println("[VAPD] experimental provider enabled but PV entity is unavailable; skipping")
		} else {
			vapdProvider = usecases.NewVAPDProvider(pvEntity, bus, cfg.Logging.DebugEvents)
			if err := bridgeSvc.Service().AddUseCase(vapdProvider.UseCase()); err != nil {
				log.Fatalf("adding VAPD use case: %v", err)
			}
			log.Println("[VAPD] experimental PV-system provider registered; awaiting PV data via VisualizationService")
			registeredUseCases = append(registeredUseCases, "VAPD")
		}
	}

	// SPIKE: experimental VABD (battery) display provider. Off by default.
	var vabdProvider *usecases.VABDProvider
	if cfg.Experimental.VABDProvider {
		batteryEntity := bridgeSvc.BatteryEntity()
		if batteryEntity == nil {
			log.Println("[VABD] experimental provider enabled but battery entity is unavailable; skipping")
		} else {
			vabdProvider = usecases.NewVABDProvider(batteryEntity, bus, cfg.Logging.DebugEvents)
			if err := bridgeSvc.Service().AddUseCase(vabdProvider.UseCase()); err != nil {
				log.Fatalf("adding VABD use case: %v", err)
			}
			log.Println("[VABD] experimental battery-system provider registered; awaiting battery data via VisualizationService")
			registeredUseCases = append(registeredUseCases, "VABD")
		}
	}
	// OHPCF (heat-pump compressor flexibility) CEM client. On by default; reads
	// the remote heat pump's optional-consumption offer and drives
	// schedule/pause/resume/abort via OHPCFService.
	var ohpcfWrapper *usecases.OHPCFWrapper
	if *cfg.OHPCF.Enabled {
		ohpcfWrapper = usecases.NewOHPCFWrapper(bus, registry, cfg.Logging.DebugEvents)
		ohpcfWrapper.Setup(localEntity)
		if err := bridgeSvc.Service().AddUseCase(ohpcfWrapper.UseCase()); err != nil {
			log.Fatalf("adding OHPCF use case: %v", err)
		}
		log.Println("[OHPCF] CEM client registered; awaiting remote compressor SmartEnergyManagementPs")
		registeredUseCases = append(registeredUseCases, "OHPCF")
	}

	// Controllable systems revert an active LPC limit to its failsafe value when
	// heartbeats stop arriving, so keep the local heartbeat running for the
	// lifetime of the bridge.
	if err := lpcWrapper.StartHeartbeat(""); err != nil {
		log.Printf("starting LPC heartbeat failed: %v", err)
	} else {
		log.Println("Started LPC heartbeat")
	}
	log.Printf("Registered EEBUS use cases: %s", strings.Join(registeredUseCases, ", "))

	grpcSrv, err := bridgegrpc.NewServerWithSecurity(
		cfg.GRPC.Bind,
		cfg.GRPC.Port,
		cfg.GRPC.EnableReflection,
		cfg.GRPC.Security,
	)
	if err != nil {
		log.Fatalf("configuring gRPC server security: %v", err)
	}

	trustController := eebus.NewTrustController(bridgeSvc, registry, bus)
	deviceSvc := bridgegrpc.NewDeviceService(bridgeSvc.Callbacks(), bus, ski, registry, trustController)
	lpcSvc := bridgegrpc.NewLPCService(lpcWrapper, bus, registry)
	monitoringSvc := bridgegrpc.NewMonitoringService(
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
	)
	gridSvc := bridgegrpc.NewGridService(mgcpProvider)
	visualizationSvc := bridgegrpc.NewVisualizationService(vapdProvider, vabdProvider)
	ohpcfSvc := bridgegrpc.NewOHPCFService(ohpcfWrapper, bus, registry)
	dhwSvc := bridgegrpc.NewDHWService(dhwTemperature, dhwSystemFunction, bus)
	hvacSvc := bridgegrpc.NewHVACService(
		roomHeatingTemperature,
		roomHeatingSystemFunction,
		roomMonitoringWrapper,
		bus,
	)

	pb.RegisterDeviceServiceServer(grpcSrv.GRPCServer(), deviceSvc)
	pb.RegisterLPCServiceServer(grpcSrv.GRPCServer(), lpcSvc)
	pb.RegisterMonitoringServiceServer(grpcSrv.GRPCServer(), monitoringSvc)
	// OHPCF control (schedule/pause/resume/abort) is a command surface like LPC
	// write, not a reading-injection provider, so it is registered alongside the
	// other control services rather than behind the loopback push gate below.
	pb.RegisterOHPCFServiceServer(grpcSrv.GRPCServer(), ohpcfSvc)
	pb.RegisterDHWServiceServer(grpcSrv.GRPCServer(), dhwSvc)
	pb.RegisterHVACServiceServer(grpcSrv.GRPCServer(), hvacSvc)

	// Provider push services are safe on loopback or behind tls_token auth.
	if bridgegrpc.RegisterPushServices(grpcSrv, cfg.GRPC.Bind, cfg.GRPC.Security.Mode, gridSvc, visualizationSvc) {
		log.Println("Registered provider push services (grid/PV/battery)")
	} else {
		log.Printf("Refusing to register provider push services: gRPC bind %q is not secured", cfg.GRPC.Bind)
	}

	go func() {
		log.Printf("gRPC server listening on %s:%d", cfg.GRPC.Bind, cfg.GRPC.Port)
		if err := grpcSrv.Start(); err != nil {
			log.Fatalf("gRPC server: %v", err)
		}
	}()

	if err := bridgeSvc.Start(); err != nil {
		log.Fatalf("EEBUS service start: %v", err)
	}
	log.Println("EEBUS bridge started")

	// SPIKE: trust a known remote SKI at startup so a test container can complete
	// the SHIP handshake without Home Assistant sending device.register_ski.
	if cfg.Experimental.TrustSKI != "" {
		bridgeSvc.RegisterRemoteSKI(cfg.Experimental.TrustSKI)
		log.Printf("[EXP] auto-trusted remote SKI: %s", cfg.Experimental.TrustSKI)
	}
	if cfg.Logging.DebugEvents {
		log.Println("[DEBUG] EEBUS event debug logging enabled; waiting for incoming callbacks")
	}

	shutdownCh := make(chan string, 1)
	go func() {
		ticker := time.NewTicker(watchdogInterval)
		defer ticker.Stop()
		for range ticker.C {
			staleDevices := registry.StaleDevices(monitoringStaleThreshold, monitoringGracePeriod)
			grpcSrv.SetHealthy(len(staleDevices) == 0)
			if len(staleDevices) > 0 {
				details := make([]string, 0, len(staleDevices))
				for _, ski := range staleDevices {
					lastSuccessAge := "never"
					if age, ok := registry.MonitoringLastSuccessAge(ski); ok {
						lastSuccessAge = age.Round(time.Second).String()
					}
					details = append(details, eebus.ShortSKI(ski)+"(last_success_age="+lastSuccessAge+")")
				}

				reason := "monitoring watchdog detected stale connected devices"
				log.Printf(
					"%s: devices=[%s] threshold=%s grace_period=%s; requesting restart",
					reason,
					strings.Join(details, ", "),
					monitoringStaleThreshold,
					monitoringGracePeriod,
				)
				shutdownCh <- reason
				return
			}
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	exitCode := 0
	select {
	case sig := <-sigCh:
		log.Printf("Received signal %s", sig)
	case reason := <-shutdownCh:
		exitCode = 1
		log.Printf("Controlled shutdown requested: %s", reason)
	}

	log.Println("Shutting down...")
	if err := lpcWrapper.StopHeartbeat(); err != nil {
		log.Printf("stopping LPC heartbeat failed: %v", err)
	}
	grpcSrv.Stop()
	bridgeSvc.Shutdown()
	log.Println("Shutdown complete")
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}
