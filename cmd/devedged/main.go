package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version.String())
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	srv := daemon.NewServer(
		daemon.WithServerLogger(logger),
	)

	logger.Info("starting devedged", "version", version.String())
	if err := srv.Run(ctx); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
	logger.Info("devedged stopped")
}
