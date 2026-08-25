package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"pdf2epub/internal/app"
	"pdf2epub/internal/artifactstore"
	"pdf2epub/internal/auth"
	"pdf2epub/internal/config"
	"pdf2epub/internal/converter"
	"pdf2epub/internal/httpapi"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	conversionService, err := converter.New(converter.Config{
		MaxPages:             cfg.MaxPages,
		EPUBCheckCommand:     cfg.EPUBCheckCommand,
		RequireEPUBCheck:     cfg.RequireEPUBCheck,
		CoverDPI:             120,
		IllustrationDPI:      110,
		MaxIllustrationsPage: 12,
	})
	if err != nil {
		logger.Error("initialize converter", "error", err)
		os.Exit(1)
	}
	var store app.ArtifactStore
	if cfg.R2Enabled {
		store, err = artifactstore.NewR2(artifactstore.Config{
			Endpoint:        "https://" + cfg.R2AccountID + ".r2.cloudflarestorage.com",
			AccessKeyID:     cfg.R2AccessKeyID,
			SecretAccessKey: cfg.R2SecretKey,
			Bucket:          cfg.R2Bucket,
			Prefix:          cfg.R2Prefix,
		})
		if err != nil {
			logger.Error("initialize R2 artifact store", "error", err)
			os.Exit(1)
		}
		logger.Info("R2 artifact delivery enabled", "bucket", cfg.R2Bucket, "prefix", cfg.R2Prefix)
	}
	jobs, err := app.NewManager(app.ManagerConfig{
		WorkDir: cfg.WorkDir, MaxUploadBytes: cfg.MaxUploadBytes,
		JobTimeout: cfg.JobTimeout, Retention: cfg.Retention,
		DownloadURLTTL: cfg.DownloadURLTTL, ArtifactStore: store,
	}, conversionService)
	if err != nil {
		logger.Error("initialize job manager", "error", err)
		os.Exit(1)
	}
	defer jobs.Close()

	sessions := auth.NewManager(cfg.Username, cfg.Password, cfg.SessionTTL, cfg.SessionSecret)
	api := httpapi.New(sessions, jobs, cfg.MaxUploadBytes, cfg.SecureCookie)
	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           api.Handler(),
		ReadHeaderTimeout: cfg.ShutdownTimeout,
		ReadTimeout:       cfg.JobTimeout,
		WriteTimeout:      cfg.JobTimeout,
		IdleTimeout:       cfg.SessionTTL,
		MaxHeaderBytes:    1 << 20,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		logger.Info("shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
	}()

	logger.Info("server started", "address", cfg.Address, "secure_cookie", cfg.SecureCookie)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped unexpectedly", "error", err)
		os.Exit(1)
	}
}
