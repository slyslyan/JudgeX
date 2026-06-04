package main

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"judgex/internal/cache"
	"judgex/internal/config"
	"judgex/internal/database"
	"judgex/internal/middleware"
	"judgex/internal/queue"
	"judgex/internal/sandbox"
	"judgex/internal/tracing"
	"judgex/internal/worker"
)

func main() {
	// Sandbox init: cgroups + seccomp (must happen first)
	if sandbox.SandboxInit() {
		return
	}

	cfg := config.Load()
	worker.TestDataPath = cfg.TestDataPath

	middleware.InitLogger()

	// Production safety check
	if fatal, warns := cfg.ProductionCheck(); len(fatal) > 0 {
		for _, w := range warns {
			slog.Warn("config: " + w)
		}
		if os.Getenv("INSECURE") != "1" {
			for _, f := range fatal {
				slog.Error("[FATAL] " + f)
			}
			os.Exit(1)
		}
		slog.Warn("INSECURE mode enabled — running with default secrets")
	}
	middleware.InitAuth(cfg)
	database.Init(cfg)
	cache.Init()

	shutdownTracing := tracing.Init()
	defer shutdownTracing()

	slog.Info("Judge worker starting",
		"nsqd_addr", cfg.NSQAddr,
		"test_data_path", cfg.TestDataPath,
	)

	// Start consuming judge tasks from NSQ
	queue.Init(worker.JudgeTask)

	// Wait for shutdown signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	slog.Info("Judge worker ready, waiting for tasks...")
	<-sig

	slog.Info("Judge worker shutting down")
	queue.Stop()
}
