package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/freetetra/server/internal/config"
	"github.com/freetetra/server/internal/service"
	webassets "github.com/freetetra/server/web"
)

func main() {
	webRoot := flag.String("web-root", "", "Path to a built Vue SPA (web/dist). Overrides WEB_ROOT. Ignored in -tags web_embed builds.")
	flag.Parse()

	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}
	if *webRoot != "" {
		cfg.WebRoot = *webRoot
	}
	if cfg.WebRoot != "" {
		webassets.SetRoot(cfg.WebRoot)
	}

	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)
	if webassets.EmbedMode() {
		logger.Printf("web assets: embedded (build tag web_embed)")
	} else if cfg.WebRoot != "" {
		logger.Printf("web assets: serving from --web-root %s", cfg.WebRoot)
	} else {
		logger.Printf("web assets: not configured (pass --web-root or set WEB_ROOT to enable /spa)")
	}

	svc, err := service.New(cfg, logger)
	if err != nil {
		logger.Fatalf("service init error: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := svc.Run(ctx); err != nil {
		logger.Fatalf("service error: %v", err)
	}
}
