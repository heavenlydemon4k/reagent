// cmd/worker is the polling worker entry point for the Ingestion Mesh.
// It polls configured email accounts for new messages and triggers ingestion.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/decisionstack/ingestion/internal/config"
	"github.com/decisionstack/ingestion/internal/contact"
	"github.com/decisionstack/ingestion/internal/crypto"
	"github.com/decisionstack/ingestion/internal/db"
	eventspkg "github.com/decisionstack/ingestion/internal/events"
	"github.com/decisionstack/ingestion/internal/fetch"
	"github.com/decisionstack/ingestion/internal/logger"
	"github.com/decisionstack/ingestion/internal/models"
	natspkg "github.com/decisionstack/ingestion/internal/nats"
	"github.com/decisionstack/ingestion/internal/oauth"
	"github.com/decisionstack/ingestion/internal/parse"
	"github.com/decisionstack/ingestion/internal/poll"
	"github.com/decisionstack/ingestion/internal/redis"
	s3client "github.com/decisionstack/ingestion/internal/s3"
	"github.com/decisionstack/ingestion/internal/thread"

	"github.com/google/uuid"
	neo4j "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "worker error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Initialize logger
	logger.Init(cfg)
	log := logger.L().With("service", "worker")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Info(ctx, "starting ingestion worker", "version", cfg.AppVersion, "environment", cfg.Environment)

	// Initialize database
	database, err := db.New(cfg)
	if err != nil {
		log.Error(ctx, "failed to connect to database", "error", err)
		return fmt.Errorf("init database: %w", err)
	}
	defer database.Close()
	log.Info(ctx, "database connected")

	// Initialize Redis
	redisClient, err := redis.New(cfg)
	if err != nil {
		log.Error(ctx, "failed to connect to redis", "error", err)
		return fmt.Errorf("init redis: %w", err)
	}
	defer redisClient.Close()
	log.Info(ctx, "redis connected")

	// Initialize NATS publisher
	natsPublisher, err := natspkg.NewJetStreamPublisher(cfg.NATSURL)
	if err != nil {
		log.Error(ctx, "failed to connect to NATS", "error", err)
		return fmt.Errorf("init nats: %w", err)
	}
	defer natsPublisher.Close()
	log.Info(ctx, "nats connected")

	// Initialize S3 client for raw email storage
	s3Client, err := s3client.NewClient(cfg)
	if err != nil {
		log.Error(ctx, "failed to initialize S3 client", "error", err)
		return fmt.Errorf("init s3: %w", err)
	}
	log.Info(ctx, "s3 connected")

	// Initialize KMS client for token encryption
	kmsClient, err := crypto.NewKMSClient(cfg)
	if err != nil {
		log.Error(ctx, "failed to initialize KMS client", "error", err)
		return fmt.Errorf("init kms: %w", err)
	}
	defer kmsClient.Close()

	// Initialize token crypto for OAuth token encryption/decryption
	tokenCrypto := crypto.NewTokenCrypto(kmsClient)

	// Initialize slog logger for poll package (before Neo4j so it can be passed in)
	slogLogger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slogLevelFromString(cfg.LogLevel),
	}))

	// Initialize Neo4j for thread reconstruction and contact dedup
	neo4jDriver, err := neo4j.NewDriverWithContext(
		cfg.Neo4jURI,
		neo4j.BasicAuth(cfg.Neo4jUser, cfg.Neo4jPassword, ""),
	)
	if err != nil {
		log.Error(ctx, "failed to connect to Neo4j", "error", err)
		return fmt.Errorf("init neo4j: %w", err)
	}
	defer neo4jDriver.Close(ctx)
	log.Info(ctx, "neo4j connected")

	// Initialize event assembler: thread engine + contact dedup + raw_emails insert
	neo4jContactStore := contact.NewNeo4jStore(neo4jDriver)
	dedupEngine := contact.NewDedupEngine(neo4jContactStore, slogLogger)
	threadEngine := thread.NewEngine(database.Pool(), neo4jDriver, slogLogger)
	assembler := eventspkg.NewAssembler(database.Pool(), threadEngine, dedupEngine, slogLogger)

	// -------------------------------------------------------------------------
	// Polling Worker Pool — Blocker #4
	// -------------------------------------------------------------------------

	// Initialize OAuth token store for polling
	oauthTokenStore := oauth.NewTokenStore(database.Pool(), tokenCrypto)

	// Register OAuth providers for token refresh
	for _, name := range oauth.ProviderNames() {
		provider, err := oauth.NewProvider(name, cfg)
		if err != nil {
			log.Error(ctx, "failed to create OAuth provider", "provider", name, "error", err)
			return fmt.Errorf("init provider %s: %w", name, err)
		}
		oauthTokenStore.RegisterProvider(string(name), provider)
		log.Info(ctx, "OAuth provider registered", "provider", name)
	}

	// Initialize rate limiter for Gmail/Outlook API quotas
	rateLimiter := poll.NewRateLimiter(redisClient.Client())

	// Initialize state store for polling state (history_id, delta_link)
	stateStore := poll.NewStateStore(database.Pool())

	// Initialize MIME parser for email parsing
	mimeParser := parse.NewParser(cfg, s3Client)

	// Create real API fetchers.
	gmailFetcher := fetch.NewGmailAPIFetcher(slogLogger)
	outlookFetcher := fetch.NewOutlookAPIFetcher(slogLogger)

	// Create the Gmail and Outlook pollers — both implement poll.JobProcessor
	gmailPoller := poll.NewGmailPoller(
		rateLimiter,
		stateStore,
		gmailFetcher,
		&tokenStoreAdapter{store: oauthTokenStore},
		&mimeParserAdapter{parser: mimeParser},
		natsPublisher,
		assembler,
		slogLogger,
	)

	outlookPoller := poll.NewOutlookPoller(
		rateLimiter,
		stateStore,
		outlookFetcher,
		&tokenStoreAdapter{store: oauthTokenStore},
		&mimeParserAdapter{parser: mimeParser},
		natsPublisher,
		assembler,
		cfg.MicrosoftClientID, // app ID for rate limiting
		slogLogger,
	)

	// Composite processor: dispatches to the correct poller based on provider
	compositeProcessor := &compositeJobProcessor{
		gmail:   gmailPoller,
		outlook: outlookPoller,
		log:     slogLogger,
	}

	// Create and start the worker pool
	workerPool := poll.NewWorkerPool(4, slogLogger) // 4 concurrent polling workers
	workerPool.Start(ctx, compositeProcessor)
	log.Info(ctx, "polling worker pool started", "size", 4)

	// Create and start the scheduler — queries DB for due accounts
	scheduler := poll.NewScheduler(
		database.Pool(),
		workerPool,
		cfg.PollIntervalDefault,
		slogLogger,
	)
	if err := scheduler.Start(ctx); err != nil {
		log.Error(ctx, "failed to start scheduler", "error", err)
		return fmt.Errorf("start scheduler: %w", err)
	}
	log.Info(ctx, "polling scheduler started", "interval", cfg.PollIntervalDefault)

	// -------------------------------------------------------------------------
	// Send Consumer — listens for email.send and dispatches via Gmail/Outlook
	// -------------------------------------------------------------------------

	googleSendProvider, _ := oauth.NewProvider(oauth.ProviderGmail, cfg)
	outlookSendProvider, _ := oauth.NewProvider(oauth.ProviderOutlook, cfg)

	sendConsumer := natspkg.NewSendConsumer(
		oauthTokenStore,
		googleSendProvider,
		outlookSendProvider,
		database.Pool(),
		natsPublisher.JetStream(),
		log,
	)

	go func() {
		if err := sendConsumer.Subscribe(ctx); err != nil {
			log.Error(ctx, "send consumer error", "error", err)
		}
	}()
	log.Info(ctx, "send consumer started")

	// -------------------------------------------------------------------------
	// Graceful shutdown
	// -------------------------------------------------------------------------

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	<-stop
	log.Info(ctx, "shutdown signal received, gracefully shutting down")
	cancel()

	// Stop scheduler first (no more new jobs)
	if err := scheduler.Stop(); err != nil {
		log.Error(ctx, "scheduler stop error", "error", err)
	}

	// Stop worker pool (let current jobs finish)
	if err := workerPool.Stop(); err != nil {
		log.Error(ctx, "worker pool stop error", "error", err)
	}

	log.Info(ctx, "worker stopped gracefully")
	return nil
}

// ---------------------------------------------------------------------------
// slog level helper
// ---------------------------------------------------------------------------

func slogLevelFromString(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ---------------------------------------------------------------------------
// Composite Job Processor — dispatches to Gmail or Outlook poller
// ---------------------------------------------------------------------------

type compositeJobProcessor struct {
	gmail   *poll.GmailPoller
	outlook *poll.OutlookPoller
	log     *slog.Logger
}

func (c *compositeJobProcessor) Process(ctx context.Context, job poll.FetchJob) error {
	switch job.Provider {
	case "gmail":
		return c.gmail.Process(ctx, job)
	case "outlook":
		return c.outlook.Process(ctx, job)
	default:
		c.log.Warn("unknown provider in fetch job", "provider", job.Provider, "account_id", job.AccountID)
		return fmt.Errorf("unknown provider: %s", job.Provider)
	}
}

// ---------------------------------------------------------------------------
// Token Store Adapter — adapts oauth.TokenStore to poll.TokenStore interface
// ---------------------------------------------------------------------------

type tokenStoreAdapter struct {
	store *oauth.TokenStore
}

func (a *tokenStoreAdapter) GetTokens(ctx context.Context, accountID uuid.UUID) (*models.TokenPair, error) {
	return a.store.GetTokens(ctx, accountID)
}

func (a *tokenStoreAdapter) RefreshIfNeeded(ctx context.Context, accountID uuid.UUID) (*models.TokenPair, error) {
	return a.store.RefreshIfNeeded(ctx, accountID)
}

// ---------------------------------------------------------------------------
// MIME Parser Adapter — adapts parse.Parser to poll.MIMEParser interface
// ---------------------------------------------------------------------------

type mimeParserAdapter struct {
	parser *parse.Parser
}

func (a *mimeParserAdapter) Parse(raw []byte, accountID, userID uuid.UUID) (*models.ParsedEmail, error) {
	// Bridge poll.MIMEParser (no ctx, no receivedAt) to parse.Parser (needs both).
	// Use context.Background() — the caller's context isn't available at this interface level.
	// Use time.Now().UTC() as receivedAt — the email was just received.
	// NOTE: parameter order differs: MIMEParser is (raw, accountID, userID) but
	// Parser.Parse() is (ctx, rawMIME, userID, accountID, receivedAt) — swap the IDs.
	return a.parser.Parse(context.Background(), raw, userID, accountID, time.Now().UTC())
}

// ---------------------------------------------------------------------------
// Outlook fetcher is now provided by github.com/decisionstack/ingestion/internal/fetch
// via fetch.NewOutlookAPIFetcher().
// ---------------------------------------------------------------------------