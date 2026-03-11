package main

import (
	"context"
	"flag"
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

	var (
		tcpAddr       = flag.String("tcp", daemon.DefaultTCPAddr(), "TCP address for admin API and dashboard")
		hostsPath     = flag.String("hosts", "/etc/hosts", "path to hosts file for DNS management")
		manageTraefik = flag.Bool("traefik", false, "manage Traefik subprocess lifecycle")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	opts := []daemon.ServerOption{
		daemon.WithServerLogger(logger),
		daemon.WithTCPAddr(*tcpAddr),
		daemon.WithHostsPath(*hostsPath),
		daemon.WithManageTraefik(*manageTraefik),
	}

	srv := daemon.NewServer(opts...)

	logger.Info("starting devedged", "version", version.String())
	if err := srv.Run(ctx); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
	logger.Info("devedged stopped")
}
