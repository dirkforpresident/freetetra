package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/freetetra/server/internal/config"
	"github.com/freetetra/server/internal/service"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)
	groups := make([]uint32, 0, 1)
	if cfg.WebRadio.Talkgroup > 0 {
		groups = append(groups, cfg.WebRadio.Talkgroup)
	}
	plane := service.NewBrewModulePlane(cfg, logger, cfg.WebRadio.BrewISSI, groups)

	bridge, err := service.NewWebRadioBridge(cfg, logger, plane)
	if err != nil {
		logger.Fatalf("webradio init error: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		if err := plane.Run(ctx); err != nil {
			logger.Printf("brew module plane error: %v", err)
			cancel()
		}
	}()

	if err := bridge.Start(ctx); err != nil {
		logger.Fatalf("webradio start error: %v", err)
	}
	defer bridge.Stop()

	<-ctx.Done()
}
