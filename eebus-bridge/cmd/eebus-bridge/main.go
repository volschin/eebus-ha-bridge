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

// monitoringStaleThreshold bounds how long a trusted device may go without a
// successful Monitoring entity resolution before the watchdog restarts the
// process. Reconnects (SHIP re-pair) can leave the SPINE entity binding stuck
// with no error logged, silently starving Home Assistant of data; a restart
// forces a clean re-handshake. The device normally pushes updates ~every 60s,
// so 10min gives ample margin against brief legitimate gaps.
const monitoringStaleThreshold = 10 * time.Minute

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
	registeredUseCases := []string{
		"LPC", "Monitoring", "DHWMonitoring", "MRT", "MOT", "DHWTemperature", "DHWSystemFunction",
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

	// SPIKE: experimental read-only HVAC/DHW probe. Off by default. Must be armed
	// before Start so its Setpoint/HVAC client features are part of the announced
	// feature map.
	if cfg.Experimental.HvacProbe {
		eebus.DefaultHvacProbe().Setup(localEntity)
		log.Println("[HVACPROBE] experimental HVAC/DHW read probe armed; will dump Setpoint/HVAC data on device connect")
		if cfg.Experimental.HvacProbeBind {
			eebus.DefaultHvacProbe().EnableBind()
			log.Println("[HVACPROBE] stage-2 bind armed; will request Setpoint/HVAC bindings on device connect")
			if cfg.Experimental.HvacProbeWrite {
				eebus.DefaultHvacProbe().EnableWrite()
				log.Println("[HVACPROBE] stage-3 write armed; will echo-write current setpoint data after accepted bind (values unchanged)")
				if ski := cfg.Experimental.HvacProbeWriteDeltaSKI; ski != "" {
					eebus.DefaultHvacProbe().EnableWriteDelta(ski)
					log.Printf("[HVACPROBE] stage-3b delta armed for SKI %s; will change DHW setpoint one step, confirm, and restore", ski)
				}
			} else if cfg.Experimental.HvacProbeWriteDeltaSKI != "" {
				log.Println("[HVACPROBE] hvac_probe_write_delta_ski requires hvac_probe_write; delta stage not armed")
			}
			if ski := cfg.Experimental.HvacProbeOverrunWriteSKI; ski != "" {
				eebus.DefaultHvacProbe().EnableOverrunWrite(ski)
				log.Printf("[HVACPROBE] stage-4b DHW boost armed for SKI %s; will activate one-time DHW overrun, confirm, and cancel after accepted HVAC bind", ski)
			}
		} else if cfg.Experimental.HvacProbeWrite {
			log.Println("[HVACPROBE] hvac_probe_write requires hvac_probe_bind; write stage not armed")
		} else if cfg.Experimental.HvacProbeOverrunWriteSKI != "" {
			log.Println("[HVACPROBE] hvac_probe_overrun_write_ski requires hvac_probe_bind; boost stage not armed")
		}
	} else if cfg.Experimental.HvacProbeBind || cfg.Experimental.HvacProbeWrite || cfg.Experimental.HvacProbeOverrunWriteSKI != "" {
		log.Println("[HVACPROBE] hvac_probe_bind/hvac_probe_write/hvac_probe_overrun_write_ski require hvac_probe; not armed")
	}

	// Opt-in extended diagnostics capture. Setup must happen before Start so the
	// read-only client features are part of detailed discovery. The capture is
	// triggered by the first remote use-case event after discovery completes.
	if cfg.Experimental.ExtendedCapture {
		eebus.DefaultExtendedCapture().Setup(localEntity, cfg.Experimental.ExtendedCaptureDir)
		log.Printf("[EXTCAPTURE] read-only extended capture armed; redacted artifacts will be written to %s", cfg.Experimental.ExtendedCaptureDir)
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

	go func() {
		ch := bus.Subscribe()
		defer bus.Unsubscribe(ch)
		for evt := range ch {
			switch evt.Type {
			case "device.register_ski":
				bridgeSvc.RegisterRemoteSKI(evt.SKI)
				log.Printf("Registered remote SKI: %s", evt.SKI)
			case "device.unregister_ski":
				// eebus-go's UnregisterRemoteService only notifies via
				// ServicePairingDetailUpdate, not ServiceAutoTrustRemoved, so the
				// registry is cleared explicitly here rather than relying on a
				// callback (cf. ServiceAutoTrustRemoved in callbacks.go, which
				// handles the remote-initiated revocation case).
				bridgeSvc.UnregisterRemoteSKI(evt.SKI)
				registry.RemoveDevice(evt.SKI)
				bus.Publish(eebus.Event{SKI: evt.SKI, Type: "device.trust_removed"})
				log.Printf("Unregistered remote SKI: %s", evt.SKI)
			}
		}
	}()

	grpcSrv := bridgegrpc.NewServer(cfg.GRPC.Bind, cfg.GRPC.Port, cfg.GRPC.EnableReflection)

	deviceSvc := bridgegrpc.NewDeviceService(bridgeSvc.Callbacks(), bus, ski, registry)
	lpcSvc := bridgegrpc.NewLPCService(lpcWrapper, bus, registry)
	monitoringSvc := bridgegrpc.NewMonitoringService(
		monitoringWrapper,
		dhwMonitoringWrapper,
		roomMonitoringWrapper,
		outdoorMonitoringWrapper,
		bus,
		registry,
	)
	gridSvc := bridgegrpc.NewGridService(mgcpProvider)
	visualizationSvc := bridgegrpc.NewVisualizationService(vapdProvider, vabdProvider)
	ohpcfSvc := bridgegrpc.NewOHPCFService(ohpcfWrapper, bus, registry)
	dhwSvc := bridgegrpc.NewDHWService(dhwTemperature, dhwSystemFunction, bus)

	pb.RegisterDeviceServiceServer(grpcSrv.GRPCServer(), deviceSvc)
	pb.RegisterLPCServiceServer(grpcSrv.GRPCServer(), lpcSvc)
	pb.RegisterMonitoringServiceServer(grpcSrv.GRPCServer(), monitoringSvc)
	// OHPCF control (schedule/pause/resume/abort) is a command surface like LPC
	// write, not a reading-injection provider, so it is registered alongside the
	// other control services rather than behind the loopback push gate below.
	pb.RegisterOHPCFServiceServer(grpcSrv.GRPCServer(), ohpcfSvc)
	pb.RegisterDHWServiceServer(grpcSrv.GRPCServer(), dhwSvc)

	// The grid/PV/battery publish RPCs inject values into EEBUS state that
	// downstream equipment consumes, and the gRPC server has no transport auth.
	// Only expose them when bound to loopback so a routable bind can't let any
	// reachable client forge grid/PV/battery readings.
	if bridgegrpc.RegisterPushServices(grpcSrv, cfg.GRPC.Bind, gridSvc, visualizationSvc) {
		log.Println("Registered provider push services (grid/PV/battery) on loopback bind")
	} else {
		log.Printf("Refusing to register provider push services: gRPC bind %q is not loopback; grid/PV/battery publish RPCs disabled", cfg.GRPC.Bind)
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

	go func() {
		ticker := time.NewTicker(watchdogInterval)
		defer ticker.Stop()
		for range ticker.C {
			stale := registry.MonitoringStale(monitoringStaleThreshold)
			grpcSrv.SetHealthy(!stale)
			if stale {
				log.Fatalf(
					"monitoring watchdog: no successful entity resolution for a trusted device in over %s; exiting for restart",
					monitoringStaleThreshold,
				)
			}
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	if err := lpcWrapper.StopHeartbeat(); err != nil {
		log.Printf("stopping LPC heartbeat failed: %v", err)
	}
	grpcSrv.Stop()
	bridgeSvc.Shutdown()
	log.Println("Shutdown complete")
}
