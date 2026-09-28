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

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/ThreeHundredBugs/anekbot/internal/anekbot"
	"github.com/ThreeHundredBugs/anekbot/internal/llm"
	"github.com/ThreeHundredBugs/anekbot/internal/logging"
	"github.com/ThreeHundredBugs/anekbot/internal/stats"
)

const (
	shutdownTimeout = 5 * time.Second
	healthzPath     = "/healthz"
)

func main() {
	cfg, err := loadConfig(os.Args[1:])
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	logLevel, err := logging.ParseLevel(cfg.logLevel)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	logging.SetLevel(logLevel)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	st := stats.New()

	var anek *anekbot.AnekHandler
	if cfg.anekEnabled {
		anek = anekbot.NewAnekHandler()
		anek.SetInline(cfg.inlineEnabled, cfg.aiJokesEnabled)
		anek.SetPromotions(cfg.promotions)
		anek.SetStats(st)
		anek.SetAdmins(anekbot.NewAdmins(cfg.adminUsernames))
	}

	var swearing *anekbot.SwearingHandler
	if cfg.swearingEnabled {
		swearing, err = anekbot.NewSwearingHandler(cfg.swearingWordsFile)
		if err != nil {
			log.Fatalf("swearing handler: %v", err)
		}
		swearing.SetStats(st)
	}

	dispatcher := anekbot.NewDispatcher(anek, swearing, nil, nil)
	if len(cfg.adminUsernames) > 0 {
		dispatcher.SetStatsHandler(anekbot.NewStatsHandler(st, cfg.adminUsernames))
	}

	opts := []bot.Option{
		bot.WithDefaultHandler(func(ctx context.Context, b *bot.Bot, update *models.Update) {
			dispatcher.Dispatch(ctx, b, update)
		}),
	}
	if cfg.webhookSecret != "" {
		opts = append(opts, bot.WithWebhookSecretToken(cfg.webhookSecret))
	}

	b, err := bot.New(cfg.botToken, opts...)
	if err != nil {
		log.Fatalf("create bot: %v", err)
	}

	me, err := b.GetMe(ctx)
	if err != nil {
		log.Fatalf("get bot info: %v", err)
	}

	var llmClient *llm.LLM
	if len(cfg.llmProviders) > 0 {
		llmClient = anekbot.NewLLM(cfg.llmLimits, cfg.llmProviders...)
		llmClient.SetRecorder(st)
	}
	if anek != nil {
		anek.SetLLM(llmClient)
		anek.SetBotUsername(me.Username)
	}
	hasQuestions := llmClient != nil && cfg.questionsEnabled
	if hasQuestions {
		questions := anekbot.NewQuestionsHandler(me.Username, llmClient)
		questions.SetStats(st)
		dispatcher.SetQuestions(questions)
	}

	dispatcher.SetHelp(anekbot.NewHelpHandler(me.Username, cfg.anekEnabled, cfg.swearingEnabled, hasQuestions))

	switch cfg.mode {
	case "poll":
		log.Println("anekbot starting in poll mode")
		b.Start(ctx)
	case "webhook":
		runWebhook(ctx, cfg, b, st)
	}
}

func runWebhook(ctx context.Context, cfg *config, b *bot.Bot, st *stats.Stats) {
	mux := http.NewServeMux()
	mux.HandleFunc(cfg.webhookPath, b.WebhookHandler())
	mux.HandleFunc(healthzPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if cfg.metricsEnabled {
		mux.Handle(cfg.metricsPath, st.Handler(cfg.metricsToken))
	}
	srv := &http.Server{
		Addr:              ":" + cfg.port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("anekbot listening on %s (webhook path %s)", srv.Addr, cfg.webhookPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server: %v", err)
		}
	}()

	go b.StartWebhook(ctx)

	<-ctx.Done()
	log.Println("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http server shutdown: %v", err)
	}
}
