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
	flag.Parse()

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

	pb.RegisterDeviceServiceServer(grpcSrv.GRPCServer(), deviceSvc)
	pb.RegisterLPCServiceServer(grpcSrv.GRPCServer(), lpcSvc)
	pb.RegisterMonitoringServiceServer(grpcSrv.GRPCServer(), monitoringSvc)

	go func() {
		log.Printf("gRPC server listening on %s:%d", cfg.GRPC.Bind, cfg.GRPC.Port)
		if err := grpcSrv.Start(); err != nil {
			log.Fatalf("gRPC server: %v", err)
		}
	}()

	bridgeSvc.Start()
	log.Println("EEBUS bridge started")
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
			case "device.unregister_ski":
				bridgeSvc.UnregisterRemoteSKI(evt.SKI)
				log.Printf("Unregistered remote SKI: %s", evt.SKI)
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
