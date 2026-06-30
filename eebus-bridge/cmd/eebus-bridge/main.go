package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/certs"
	"github.com/volschin/eebus-bridge/internal/config"
	"github.com/volschin/eebus-bridge/internal/eebus"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"github.com/volschin/eebus-bridge/internal/usecases"
)

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

	if err := bridgeSvc.Setup(); err != nil {
		log.Fatalf("setting up EEBUS service: %v", err)
	}

	localEntity := bridgeSvc.LocalEntity()
	if localEntity == nil {
		log.Fatal("local CEM entity is not available")
	}
	lpcWrapper.Setup(localEntity)
	monitoringWrapper.Setup(localEntity)
	bridgeSvc.Service().AddUseCase(lpcWrapper.UseCase())
	bridgeSvc.Service().AddUseCase(monitoringWrapper.UseCase())

	// SPIKE: experimental MGCP grid-connection-point provider. Off by default.
	var mgcpProvider *usecases.MGCPProvider
	if cfg.Experimental.MGCPProvider {
		gridEntity := bridgeSvc.GridEntity()
		if gridEntity == nil {
			log.Println("[MGCP] experimental provider enabled but grid entity is unavailable; skipping")
		} else {
			mgcpProvider = usecases.NewMGCPProvider(gridEntity, bus, cfg.Logging.DebugEvents)
			bridgeSvc.Service().AddUseCase(mgcpProvider.UseCase())
			log.Println("[MGCP] experimental grid-connection-point provider registered; awaiting grid data via GridService")
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
			bridgeSvc.Service().AddUseCase(vapdProvider.UseCase())
			log.Println("[VAPD] experimental PV-system provider registered; awaiting PV data via VisualizationService")
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
			bridgeSvc.Service().AddUseCase(vabdProvider.UseCase())
			log.Println("[VABD] experimental battery-system provider registered; awaiting battery data via VisualizationService")
		}
	}
	// SPIKE: experimental OHPCF (heat-pump compressor flexibility) CEM client.
	// Off by default. Reads the remote heat pump's optional-consumption offer and
	// drives schedule/pause/resume/abort via OHPCFService.
	var ohpcfWrapper *usecases.OHPCFWrapper
	if cfg.Experimental.OHPCFClient {
		ohpcfWrapper = usecases.NewOHPCFWrapper(bus, registry, cfg.Logging.DebugEvents)
		ohpcfWrapper.Setup(localEntity)
		bridgeSvc.Service().AddUseCase(ohpcfWrapper.UseCase())
		log.Println("[OHPCF] experimental CEM client registered; awaiting remote compressor SmartEnergyManagementPs")
	}

	// Controllable systems revert an active LPC limit to its failsafe value when
	// heartbeats stop arriving, so keep the local heartbeat running for the
	// lifetime of the bridge.
	if err := lpcWrapper.StartHeartbeat(""); err != nil {
		log.Printf("starting LPC heartbeat failed: %v", err)
	} else {
		log.Println("Started LPC heartbeat")
	}
	log.Println("Registered EEBUS use cases: LPC, Monitoring")

	grpcSrv := bridgegrpc.NewServer(cfg.GRPC.Bind, cfg.GRPC.Port, cfg.GRPC.EnableReflection)

	deviceSvc := bridgegrpc.NewDeviceService(bridgeSvc.Callbacks(), bus, ski, registry)
	lpcSvc := bridgegrpc.NewLPCService(lpcWrapper, bus, registry)
	monitoringSvc := bridgegrpc.NewMonitoringService(monitoringWrapper, bus, registry)
	gridSvc := bridgegrpc.NewGridService(mgcpProvider)
	visualizationSvc := bridgegrpc.NewVisualizationService(vapdProvider, vabdProvider)
	ohpcfSvc := bridgegrpc.NewOHPCFService(ohpcfWrapper, bus, registry)

	pb.RegisterDeviceServiceServer(grpcSrv.GRPCServer(), deviceSvc)
	pb.RegisterLPCServiceServer(grpcSrv.GRPCServer(), lpcSvc)
	pb.RegisterMonitoringServiceServer(grpcSrv.GRPCServer(), monitoringSvc)
	// OHPCF control (schedule/pause/resume/abort) is a command surface like LPC
	// write, not a reading-injection provider, so it is registered alongside the
	// other control services rather than behind the loopback push gate below.
	pb.RegisterOHPCFServiceServer(grpcSrv.GRPCServer(), ohpcfSvc)

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
		ch := bus.Subscribe()
		defer bus.Unsubscribe(ch)
		for evt := range ch {
			switch evt.Type {
			case "device.register_ski":
				bridgeSvc.RegisterRemoteSKI(evt.SKI)
				log.Printf("Registered remote SKI: %s", evt.SKI)
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
