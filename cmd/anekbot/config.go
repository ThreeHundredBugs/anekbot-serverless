package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ThreeHundredBugs/anekbot/internal/anekbot"
	"github.com/ThreeHundredBugs/anekbot/internal/llm"
	"github.com/ThreeHundredBugs/anekbot/internal/logging"
)

type config struct {
	botToken      string
	mode          string
	logLevel      string
	port          string
	webhookPath   string
	webhookSecret string

	prometheusEnabled bool
	prometheusPath    string
	prometheusToken   string

	// adminUsernames are Telegram @handles (no "@")
	adminUsernames []string

	// persistenceFile is where stats are saved on shutdown and loaded from on startup;
	// empty disables persistence.
	persistenceFile string

	// llmProviders is tried in order; empty means no LLM.
	llmProviders    []llm.Provider
	llmLimits       llm.Limits
	llmSystemPrompt string

	aiJokePromptTemplate string

	anekEnabled       bool
	inlineEnabled     bool
	aiJokesEnabled    bool
	promotions        *anekbot.Promotions
	questionsEnabled  bool
	swearingEnabled   bool
	swearingWordsFile string
}

type fileConfig struct {
	Bot struct {
		Token    string `json:"token"`
		Mode     string `json:"mode"`
		LogLevel string `json:"log_level"`
	} `json:"bot"`
	Server struct {
		Port          string `json:"port"`
		WebhookPath   string `json:"webhook_path"`
		WebhookSecret string `json:"webhook_secret"`
	} `json:"server"`
	Stats struct {
		// PersistenceFile is where stats are saved on shutdown and loaded from on startup;
		// empty disables persistence.
		PersistenceFile string `json:"persistence_file"`
		Prometheus      struct {
			Enabled *bool  `json:"enabled"`
			Path    string `json:"path"`
			Token   string `json:"token"`
		} `json:"prometheus"`
	} `json:"stats"`
	Admin struct {
		// Usernames are Telegram @handles trusted with the /stats command.
		Usernames []string `json:"usernames"`
	} `json:"admin"`
	LLM struct {
		Providers []providerConfig `json:"providers"`
		RateLimit rateLimitConfig  `json:"rate_limit"`
		// SystemPrompt is sent to the LLM for both question-answering and AI joke generation.
		SystemPrompt string `json:"system_prompt"`
	} `json:"llm"`
	Anek struct {
		Enabled *bool `json:"enabled"`
		Inline  struct {
			Enabled    *bool                     `json:"enabled"`
			AIJokes    *bool                     `json:"ai_jokes"`
			Promotions *anekbot.PromotionsConfig `json:"promotions"`
			// AIJokePromptTemplate must contain exactly one %s, replaced with the requested topic.
			AIJokePromptTemplate string `json:"ai_joke_prompt_template"`
		} `json:"inline"`
	} `json:"anek"`
	Questions struct {
		Enabled *bool `json:"enabled"`
	} `json:"questions"`
	Swearing struct {
		Enabled   *bool  `json:"enabled"`
		WordsFile string `json:"words_file"`
	} `json:"swearing"`
}

type providerConfig struct {
	Type  string `json:"type"`
	Model string `json:"model"`
	// APIKeyEnv overrides the default env var for Type.
	APIKeyEnv string `json:"api_key_env"`
	// APIKey sets the key directly in the config file, taking precedence over APIKeyEnv.
	APIKey string `json:"api_key"`
}

type rateLimitConfig struct {
	MaxConcurrent        int `json:"max_concurrent"`
	PerUserLimit         int `json:"per_user_limit"`
	PerUserWindowSeconds int `json:"per_user_window_seconds"`
	MaxUsers             int `json:"max_users"`
	PruneIntervalSeconds int `json:"prune_interval_seconds"`
}

func (c rateLimitConfig) toLimits() llm.Limits {
	return llm.Limits{
		MaxConcurrent: c.MaxConcurrent,
		PerUserLimit:  c.PerUserLimit,
		PerUserWindow: time.Duration(c.PerUserWindowSeconds) * time.Second,
		MaxUsers:      c.MaxUsers,
		PruneInterval: time.Duration(c.PruneIntervalSeconds) * time.Second,
	}
}

var defaultAPIKeyEnv = map[string]string{
	"gemini":      "GEMINI_API_KEY",
	"huggingface": "HF_API_KEY",
}

func buildProvider(pc providerConfig) (llm.Provider, error) {
	defEnv, ok := defaultAPIKeyEnv[pc.Type]
	if !ok {
		return nil, fmt.Errorf("llm.providers: unknown type %q: must be %q or %q", pc.Type, "gemini", "huggingface")
	}

	if pc.APIKey != "" && pc.APIKeyEnv != "" {
		logging.Warnf("llm provider %s: both api_key and api_key_env are set; using api_key (this may be unintentional)", pc.Type)
	}

	key := pc.APIKey
	if key == "" {
		keyEnv := or(pc.APIKeyEnv, defEnv)
		key = os.Getenv(keyEnv)
		if key == "" {
			logging.Warnf("llm provider %s skipped: env var %s is empty", pc.Type, keyEnv)
			return nil, nil
		}
	}
	if pc.Type == "gemini" {
		return llm.NewGeminiProvider(key, pc.Model), nil
	}
	return llm.NewHuggingFaceProvider(key, pc.Model), nil
}

func loadFileConfig(path string) (*fileConfig, error) {
	if path == "" {
		return &fileConfig{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var fc fileConfig
	if err := dec.Decode(&fc); err != nil {
		return nil, fmt.Errorf("parse config file %s: %w", path, err)
	}
	return &fc, nil
}

func enabled(v *bool) bool {
	return v == nil || *v
}

func loadConfig(args []string) (*config, error) {
	fs := flag.NewFlagSet("anekbot", flag.ContinueOnError)
	// The config file's own location can't live inside that file, so -config/ANEKBOT_CONFIG
	// is the one setting that doesn't follow the file > env > default precedence below.
	configPath := fs.String("config", os.Getenv("ANEKBOT_CONFIG"), "path to the JSON config file (env ANEKBOT_CONFIG)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	fc, err := loadFileConfig(*configPath)
	if err != nil {
		return nil, err
	}

	cfg := &config{
		botToken:      fileEnvDefault(fc.Bot.Token, "BOT_TOKEN", ""),
		mode:          fileEnvDefault(fc.Bot.Mode, "ANEKBOT_MODE", "poll"),
		logLevel:      fileEnvDefault(fc.Bot.LogLevel, "LOG_LEVEL", "warn"),
		port:          fileEnvDefault(fc.Server.Port, "PORT", "8080"),
		webhookPath:   or(fc.Server.WebhookPath, "/webhook"),
		webhookSecret: fileEnvDefault(fc.Server.WebhookSecret, "WEBHOOK_SECRET_TOKEN", ""),

		// The Prometheus endpoint defaults to disabled, unlike the other *.enabled flags:
		// exposing an HTTP endpoint is a deliberate opt-in, not a safe default.
		prometheusEnabled: fc.Stats.Prometheus.Enabled != nil && *fc.Stats.Prometheus.Enabled,
		prometheusPath:    or(fc.Stats.Prometheus.Path, "/metrics"),
		prometheusToken:   fileEnvDefault(fc.Stats.Prometheus.Token, "METRICS_TOKEN", ""),
		adminUsernames:    fc.Admin.Usernames,
		persistenceFile:   fileEnvDefault(fc.Stats.PersistenceFile, "ANEKBOT_STATS_PERSISTENCE_FILE", ""),

		anekEnabled:       enabled(fc.Anek.Enabled),
		inlineEnabled:     enabled(fc.Anek.Inline.Enabled),
		aiJokesEnabled:    enabled(fc.Anek.Inline.AIJokes),
		questionsEnabled:  enabled(fc.Questions.Enabled),
		swearingEnabled:   enabled(fc.Swearing.Enabled),
		swearingWordsFile: fc.Swearing.WordsFile,
		llmLimits:         fc.LLM.RateLimit.toLimits(),
		llmSystemPrompt:   or(fc.LLM.SystemPrompt, anekbot.DefaultSystemPrompt),

		aiJokePromptTemplate: or(fc.Anek.Inline.AIJokePromptTemplate, anekbot.DefaultAIJokePromptTemplate),
	}

	for _, pc := range fc.LLM.Providers {
		provider, err := buildProvider(pc)
		if err != nil {
			return nil, err
		}
		if provider != nil {
			cfg.llmProviders = append(cfg.llmProviders, provider)
		}
	}

	if fc.Anek.Inline.Promotions != nil {
		if cfg.promotions, err = anekbot.NewPromotions(*fc.Anek.Inline.Promotions); err != nil {
			return nil, err
		}
	}

	if cfg.botToken == "" {
		return nil, errors.New("bot token is required: set BOT_TOKEN or bot.token in the config file")
	}
	if cfg.mode != "webhook" && cfg.mode != "poll" {
		return nil, fmt.Errorf("invalid mode %q: must be %q or %q", cfg.mode, "webhook", "poll")
	}
	if cfg.mode == "webhook" && cfg.webhookSecret == "" {
		return nil, errors.New("webhook mode requires a secret: set WEBHOOK_SECRET_TOKEN or server.webhook_secret in the config file")
	}
	if cfg.mode == "webhook" && cfg.prometheusEnabled {
		if cfg.prometheusToken == "" {
			return nil, errors.New("prometheus endpoint requires a token: set METRICS_TOKEN or stats.prometheus.token in the config file")
		}
		if cfg.prometheusPath == cfg.webhookPath || cfg.prometheusPath == healthzPath {
			return nil, fmt.Errorf("stats.prometheus.path %q collides with an existing server route", cfg.prometheusPath)
		}
	}
	if _, err := logging.ParseLevel(cfg.logLevel); err != nil {
		return nil, err
	}
	if err := validateAIJokePromptTemplate(cfg.aiJokePromptTemplate); err != nil {
		return nil, fmt.Errorf("anek.inline.ai_joke_prompt_template: %w", err)
	}

	return cfg, nil
}

// validateAIJokePromptTemplate rejects a template that fmt.Sprintf wouldn't fill with exactly
// the joke topic: anything other than one %s verb (%% counts as a literal, not a verb).
func validateAIJokePromptTemplate(tmpl string) error {
	verbs := 0
	for i := 0; i < len(tmpl); i++ {
		if tmpl[i] != '%' {
			continue
		}
		if i+1 >= len(tmpl) {
			return fmt.Errorf("trailing %% in %q", tmpl)
		}
		switch tmpl[i+1] {
		case '%':
			i++
		case 's':
			verbs++
			i++
		default:
			return fmt.Errorf("unsupported verb %%%c in %q: only %%s and %%%% are allowed", tmpl[i+1], tmpl)
		}
	}
	if verbs != 1 {
		return fmt.Errorf("must contain exactly one %%s verb, found %d: %q", verbs, tmpl)
	}
	return nil
}

func or(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func fileEnvDefault(fileValue, envKey, def string) string {
	if fileValue != "" {
		return fileValue
	}
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return def
}
