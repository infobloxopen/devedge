// devedge-dns-webhook implements the external-dns webhook provider protocol.
//
// It runs inside a k3d cluster (or any Kubernetes cluster) and translates
// external-dns record upsert/delete operations into devedge daemon API calls.
// This makes Kubernetes Ingress objects automatically resolvable on the
// developer's host machine.
//
// Usage:
//
//	devedge-dns-webhook --devedge-url http://host.k3d.internal:15353
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/infobloxopen/devedge/internal/externaldns"
)

func main() {
	var (
		listenAddr = flag.String("listen", ":8888", "webhook listen address")
		devedgeURL = flag.String("devedge-url", "http://host.k3d.internal:15353", "devedge daemon API URL")
		domain     = flag.String("domain", "dev.test", "managed DNS domain")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	client := &externaldns.HTTPDevedgeClient{
		BaseURL: *devedgeURL,
		Client:  http.DefaultClient,
	}

	webhook := externaldns.NewWebhook(client, *domain, logger)

	srv := &http.Server{
		Addr:    *listenAddr,
		Handler: webhook.Handler(),
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()

	logger.Info("starting devedge-dns-webhook",
		"listen", *listenAddr,
		"devedge", *devedgeURL,
		"domain", *domain,
	)
	fmt.Fprintf(os.Stderr, "devedge-dns-webhook listening on %s\n", *listenAddr)

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}
