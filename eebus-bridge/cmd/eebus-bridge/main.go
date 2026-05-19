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

	bridgeSvc, err := eebus.NewBridgeService(cfg, cert, bus)
	if err != nil {
		log.Fatalf("creating bridge service: %v", err)
	}

	lpcWrapper := usecases.NewLPCWrapper(bus)
	monitoringWrapper := usecases.NewMonitoringWrapper(bus)

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
	log.Println("Registered EEBUS use cases: LPC, Monitoring")

	grpcSrv := bridgegrpc.NewServer(cfg.GRPC.Port)

	deviceSvc := bridgegrpc.NewDeviceService(bridgeSvc.Callbacks(), bus, ski)
	lpcSvc := bridgegrpc.NewLPCService(lpcWrapper, bus)
	monitoringSvc := bridgegrpc.NewMonitoringService(monitoringWrapper, bus)

	pb.RegisterDeviceServiceServer(grpcSrv.GRPCServer(), deviceSvc)
	pb.RegisterLPCServiceServer(grpcSrv.GRPCServer(), lpcSvc)
	pb.RegisterMonitoringServiceServer(grpcSrv.GRPCServer(), monitoringSvc)

	go func() {
		log.Printf("gRPC server listening on :%d", cfg.GRPC.Port)
		if err := grpcSrv.Start(); err != nil {
			log.Fatalf("gRPC server: %v", err)
		}
	}()

	bridgeSvc.Start()
	log.Println("EEBUS bridge started")

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
	grpcSrv.Stop()
	bridgeSvc.Shutdown()
	log.Println("Shutdown complete")
}
