package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Rst307/emby-service-portal/internal/app"
	"github.com/Rst307/emby-service-portal/internal/config"
	"github.com/Rst307/emby-service-portal/internal/update"
)

func main() {
	cfg, err := config.FromEnv()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}
	application, err := app.New(context.Background(), cfg)
	if err != nil {
		log.Fatalf("start application: %v", err)
	}
	defer application.Close()

	server := &http.Server{Addr: cfg.ListenAddr, Handler: application.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	serveErrors := make(chan error, 1)
	go func() {
		log.Printf("Emby user manager listening on %s", cfg.ListenAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrors <- err
		}
	}()
	workerContext, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			if err := application.RunProvisioningRecovery(workerContext); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("recover account provisioning: %v", err)
			}
			if err := application.RunExpiry(workerContext); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("run expiry scan: %v", err)
			}
			if err := application.RunPayments(workerContext); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("reconcile payment orders: %v", err)
			}
			if err := application.RunLibraryWatch(workerContext); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("scan library updates: %v", err)
			}
			select {
			case <-workerContext.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	// Self-update checker: detection always runs; with auto-update enabled it
	// also downloads, installs and requests a restart on a new release.
	updateContext, stopUpdater := context.WithCancel(context.Background())
	updateDone := make(chan struct{})
	go func() {
		defer close(updateDone)
		updaterInterval := application.Updater.Interval()
		if updaterInterval <= 0 {
			return
		}
		ticker := time.NewTicker(updaterInterval)
		defer ticker.Stop()
		for {
			select {
			case <-updateContext.Done():
				return
			case <-ticker.C:
				applied, err := application.RunUpdateTick(updateContext)
				if err != nil && !errors.Is(err, context.Canceled) {
					log.Printf("check for updates: %v", err)
				}
				if applied {
					log.Printf("auto-update applied; requesting restart")
					application.RequestRestart()
				}
			}
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	restart := false
	select {
	case <-stop:
	case <-application.RestartRequested():
		restart = true
		log.Printf("update applied; shutting down for restart")
	case err := <-serveErrors:
		log.Printf("serve HTTP: %v", err)
	}
	// Stop accepting requests before waiting for background reconciliation.
	// A deadline expiry must also close the listener rather than leaving the
	// process alive indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("shutdown HTTP server: %v", err)
		if closeErr := server.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
			log.Printf("force close HTTP server: %v", closeErr)
		}
	}
	stopWorker()
	<-workerDone
	stopUpdater()
	<-updateDone
	if restart {
		// systemd Restart=on-failure (and other supervisors) treat this code
		// as a restart request; the binary on disk is already the new one.
		log.Printf("exiting with code %d for restart", update.RestartExitCode)
		os.Exit(update.RestartExitCode)
	}
}
