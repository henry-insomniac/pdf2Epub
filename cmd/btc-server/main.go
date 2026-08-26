package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"pdf2epub/internal/abuse"
	"pdf2epub/internal/app"
	"pdf2epub/internal/artifactstore"
	"pdf2epub/internal/auth"
	"pdf2epub/internal/commerce"
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
		FixedLayoutDPI:       cfg.FixedLayoutDPI,
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
	var billing *commerce.Service
	var challenge abuse.Verifier
	var authorizeJob func(context.Context, string, string) error
	var onJobOutcome func(context.Context, app.JobOutcome)
	if cfg.PublicAccess {
		stripe, err := commerce.NewStripeGateway(commerce.StripeConfig{
			SecretKey: cfg.StripeSecretKey, WebhookSecret: cfg.StripeWebhookSecret,
		})
		if err != nil {
			logger.Error("initialize Stripe gateway", "error", err)
			os.Exit(1)
		}
		billing, err = commerce.Open(commerce.Config{
			DatabasePath: cfg.CommerceDBPath,
			PublicURL:    cfg.PublicURL,
			Pack: commerce.Pack{
				Credits: cfg.CreditPackCredits, PriceLabel: cfg.CreditPackLabel, PriceID: cfg.StripePriceID,
			},
			Gateway: stripe,
		})
		if err != nil {
			logger.Error("initialize commerce ledger", "error", err)
			os.Exit(1)
		}
		defer func() {
			if err := billing.Close(); err != nil {
				logger.Error("close commerce ledger", "error", err)
			}
		}()
		challenge, err = abuse.NewTurnstile(abuse.TurnstileConfig{SecretKey: cfg.TurnstileSecretKey})
		if err != nil {
			logger.Error("initialize Turnstile verifier", "error", err)
			os.Exit(1)
		}
		authorizeJob = billing.AuthorizeJob
		onJobOutcome = func(ctx context.Context, outcome app.JobOutcome) {
			if err := billing.RecordJobOutcome(ctx, outcome); err != nil {
				logger.Error("record billable job outcome", "job_id", outcome.JobID, "error", err)
			}
		}
		logger.Info("public paid access enabled", "public_url", cfg.PublicURL, "queue_capacity", cfg.QueueCapacity)
	}
	jobs, err := app.NewManager(app.ManagerConfig{
		WorkDir: cfg.WorkDir, MaxUploadBytes: cfg.MaxUploadBytes,
		JobTimeout: cfg.JobTimeout, Retention: cfg.Retention,
		DownloadURLTTL: cfg.DownloadURLTTL, ArtifactStore: store,
		RequireArtifactStore: cfg.PublicAccess,
		QueueCapacity:        cfg.QueueCapacity, AuthorizeJob: authorizeJob, OnJobOutcome: onJobOutcome,
	}, conversionService)
	if err != nil {
		logger.Error("initialize job manager", "error", err)
		os.Exit(1)
	}
	defer jobs.Close()

	sessions := auth.NewManager(cfg.Username, cfg.Password, cfg.SessionTTL, cfg.SessionSecret)
	api := httpapi.NewWithOptions(sessions, jobs, cfg.MaxUploadBytes, cfg.SecureCookie, httpapi.Options{
		PublicAccess: cfg.PublicAccess, Commerce: billing, Challenge: challenge, ChallengeSiteKey: cfg.TurnstileSiteKey,
		TrustProxyHeaders: cfg.PublicAccess,
	})
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
