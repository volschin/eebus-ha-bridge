package main

import (
	"context"
	"flag"
	"log"
	"os"
	"syscall"

	"github.com/volschin/eebus-bridge/internal/config"
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

	ctx, stopSignals := notifySignalContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	if err := run(ctx, cfg); err != nil {
		logRunError(err)
		os.Exit(1)
	}
}
