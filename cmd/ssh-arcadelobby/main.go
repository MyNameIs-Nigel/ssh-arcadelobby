// Command ssh-arcadelobby is the SSH front door for the ssharcade fleet:
// one public address, an arcade menu, and a transparent bridge into the
// selected game's own SSH server.
//
// Wiring: config → registry (+ health prober) → SSH server → graceful
// shutdown. See docs/README.md for the architecture and per-task specs.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/charmbracelet/ssh"

	"github.com/mynameis-nigel/ssh-arcadelobby/internal/banner"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/config"
	applog "github.com/mynameis-nigel/ssh-arcadelobby/internal/log"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/registry"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/server"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "error", err)
		os.Exit(1)
	}

	logger := applog.New(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)

	// At boot a bad games file is fatal; at runtime a bad reload keeps the
	// previous good registry.
	reg, err := registry.New(cfg.GamesPath, registry.Options{
		ProbeInterval: cfg.ProbeInterval,
		Logger:        logger,
	})
	if err != nil {
		logger.Error("games registry load failed", "path", cfg.GamesPath, "error", err)
		os.Exit(1)
	}
	reg.Start()
	defer reg.Close()

	ban, err := banner.New(cfg.BannerPath, banner.Options{
		ReloadInterval: cfg.ProbeInterval,
		Logger:         logger,
	})
	if err != nil {
		logger.Error("banner init failed", "path", cfg.BannerPath, "error", err)
		os.Exit(1)
	}
	ban.Start()
	defer ban.Close()

	st, err := store.Open(context.Background(), cfg.DBPath)
	if err != nil {
		logger.Error("store open failed", "path", cfg.DBPath, "error", err)
		os.Exit(1)
	}
	defer st.Close()

	srv, err := server.New(cfg, logger, reg, ban, st)
	if err != nil {
		logger.Error("server init failed", "error", err)
		os.Exit(1)
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, ssh.ErrServerClosed) {
			logger.Error("listen failed", "error", err)
			done <- syscall.SIGTERM
		}
	}()

	sig := <-done
	logger.Info("shutdown signal received", "signal", sig.String())

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelShutdown()

	if err := server.RunShutdownHooks(shutdownCtx); err != nil {
		logger.Error("shutdown hook failed", "error", err)
	}

	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, ssh.ErrServerClosed) {
		logger.Error("shutdown failed", "error", err)
		os.Exit(1)
	}

	logger.Info("shutdown complete")
}
