package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ThreeHundredBugs/anekbot/internal/anekbot"
	"github.com/ThreeHundredBugs/anekbot/internal/llm"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"BOT_TOKEN", "ANEKBOT_MODE", "LOG_LEVEL", "PORT", "WEBHOOK_SECRET_TOKEN", "METRICS_TOKEN",
		"ANEKBOT_CONFIG", "GEMINI_API_KEY", "HF_API_KEY", "ANEKBOT_STATS_PERSISTENCE_FILE",
	} {
		t.Setenv(k, "")
	}
}

// setWebhookEnv sets the env vars loadConfig requires to run in webhook mode.
func setWebhookEnv(t *testing.T) {
	t.Helper()
	t.Setenv("WEBHOOK_SECRET_TOKEN", "test-secret")
	t.Setenv("METRICS_TOKEN", "test-metrics-token")
}

func TestLoadConfig_ExampleFile(t *testing.T) {
	clearEnv(t)
	t.Setenv("GEMINI_API_KEY", "g")
	t.Setenv("HF_API_KEY", "h")

	cfg, err := loadConfig([]string{"-config", "../../config.example.json"})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.mode != "poll" || cfg.promotions == nil || len(cfg.llmProviders) != 2 {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	clearEnv(t)
	cfg, err := loadConfig([]string{"-config", writeConfig(t, `{"bot": {"token": "t"}}`)})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if !cfg.anekEnabled || !cfg.inlineEnabled || !cfg.aiJokesEnabled || !cfg.questionsEnabled || !cfg.swearingEnabled {
		t.Errorf("features should default to enabled: %+v", cfg)
	}
	if cfg.mode != "poll" || cfg.port != "8080" || cfg.webhookPath != "/webhook" || cfg.logLevel != "warn" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if cfg.prometheusEnabled {
		t.Error("the prometheus endpoint must default to disabled")
	}
	if len(cfg.llmProviders) != 0 {
		t.Errorf("no llm section must mean no providers, got %d", len(cfg.llmProviders))
	}
}

func TestLoadConfig_WebhookMode(t *testing.T) {
	clearEnv(t)
	setWebhookEnv(t)
	path := writeConfig(t, `{"bot": {"token": "t", "mode": "webhook"}, "stats": {"prometheus": {"enabled": true}}}`)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.mode != "webhook" {
		t.Errorf("mode = %q, want webhook", cfg.mode)
	}
	if !cfg.prometheusEnabled {
		t.Error("stats.prometheus.enabled: true must turn the prometheus endpoint on")
	}
}

func TestLoadConfig_FileOverridesEnv(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `{"bot": {"token": "file-token", "mode": "poll"}, "server": {"port": "1111"}}`)
	t.Setenv("PORT", "2222")
	t.Setenv("BOT_TOKEN", "env-token")

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.botToken != "file-token" || cfg.port != "1111" || cfg.mode != "poll" {
		t.Errorf("unexpected config: %+v, want file values to win over env", cfg)
	}
}

func TestLoadConfig_EnvUsedWhenFileFieldAbsent(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `{"bot": {"mode": "poll"}}`)
	t.Setenv("BOT_TOKEN", "env-token")
	t.Setenv("PORT", "2222")

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.botToken != "env-token" || cfg.port != "2222" {
		t.Errorf("unexpected config: %+v, want env to fill in fields the file leaves unset", cfg)
	}
}

func TestLoadConfig_ConfigPathFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("ANEKBOT_CONFIG", writeConfig(t, `{"bot": {"token": "t", "mode": "poll"}}`))

	cfg, err := loadConfig(nil)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.mode != "poll" {
		t.Errorf("mode = %q, want poll from the file named by ANEKBOT_CONFIG", cfg.mode)
	}
}

func TestLoadConfig_DisabledFeatures(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `{"bot": {"token": "t"},
		"anek": {"enabled": false, "inline": {"ai_jokes": false}},
		"questions": {"enabled": false}, "swearing": {"enabled": false}}`)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.anekEnabled || cfg.aiJokesEnabled || cfg.questionsEnabled || cfg.swearingEnabled || !cfg.inlineEnabled {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestLoadConfig_LLMProviderKeysFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("GEMINI_API_KEY", "default-gemini-key")
	t.Setenv("MY_HF_KEY", "custom-hf-key")
	path := writeConfig(t, `{"bot": {"token": "t"}, "llm": {"providers": [
		{"type": "gemini"},
		{"type": "huggingface", "api_key_env": "MY_HF_KEY"},
		{"type": "huggingface", "api_key_env": "UNSET_KEY"}
	]}}`)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if len(cfg.llmProviders) != 2 {
		t.Fatalf("providers = %d, want 2 (provider with empty key is skipped)", len(cfg.llmProviders))
	}
	if cfg.llmProviders[0].Name() != "Gemini" {
		t.Errorf("first provider = %q, want Gemini (order must be kept)", cfg.llmProviders[0].Name())
	}
}

func TestLoadConfig_LLMProviderAPIKeyInFile(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `{"bot": {"token": "t"}, "llm": {"providers": [
		{"type": "gemini", "api_key": "key-from-file"}
	]}}`)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if len(cfg.llmProviders) != 1 {
		t.Fatalf("providers = %d, want 1 (api_key alone must be enough, no env var needed)", len(cfg.llmProviders))
	}
}

func TestLoadConfig_LLMProviderGeminiThinkingBudget(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `{"bot": {"token": "t"}, "llm": {"providers": [
		{"type": "gemini", "api_key": "key-from-file", "gemini": {"thinking_budget": 0}}
	]}}`)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if len(cfg.llmProviders) != 1 {
		t.Fatalf("providers = %d, want 1", len(cfg.llmProviders))
	}
}

func TestLoadConfig_LLMProviderAPIKeyWinsOverEnv(t *testing.T) {
	clearEnv(t)
	// api_key_env points at an unset var; if it were used instead of api_key, the provider
	// would be skipped for an empty key.
	path := writeConfig(t, `{"bot": {"token": "t"}, "llm": {"providers": [
		{"type": "gemini", "api_key": "key-from-file", "api_key_env": "UNSET_KEY"}
	]}}`)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if len(cfg.llmProviders) != 1 {
		t.Fatalf("providers = %d, want 1 (api_key must win when both are set)", len(cfg.llmProviders))
	}
}

func TestLoadConfig_LLMRateLimit(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `{"bot": {"token": "t"}, "llm": {"rate_limit": {
		"max_concurrent": 4,
		"per_user_limit": 2,
		"per_user_window_seconds": 30,
		"max_users": 100,
		"prune_interval_seconds": 60
	}}}`)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	want := llm.Limits{
		MaxConcurrent: 4,
		PerUserLimit:  2,
		PerUserWindow: 30 * time.Second,
		MaxUsers:      100,
		PruneInterval: 60 * time.Second,
	}
	if cfg.llmLimits != want {
		t.Errorf("llmLimits = %+v, want %+v", cfg.llmLimits, want)
	}
}

func TestLoadConfig_LLMRateLimit_DefaultsToZeroValue(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `{"bot": {"token": "t"}}`)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.llmLimits != (llm.Limits{}) {
		t.Errorf("llmLimits = %+v, want zero value (llm.New applies its own defaults)", cfg.llmLimits)
	}
}

func TestLoadConfig_Invalid(t *testing.T) {
	clearEnv(t)
	tests := map[string]string{
		"unknown key":                           `{"bot": {"token": "t", "tokn": "x"}}`,
		"unknown provider":                      `{"bot": {"token": "t"}, "llm": {"providers": [{"type": "gpt"}]}}`,
		"bad promotions":                        `{"bot": {"token": "t"}, "anek": {"inline": {"promotions": {"frequency": 2}}}}`,
		"invalid json":                          `{`,
		"missing bot token":                     `{}`,
		"invalid mode":                          `{"bot": {"token": "t", "mode": "carrier-pigeon"}}`,
		"webhook without secret":                `{"bot": {"token": "t", "mode": "webhook"}}`,
		"prometheus without token":              `{"bot": {"token": "t", "mode": "webhook"}, "server": {"webhook_secret": "s"}, "stats": {"prometheus": {"enabled": true}}}`,
		"prometheus path collision":             `{"bot": {"token": "t", "mode": "webhook"}, "server": {"webhook_secret": "s", "webhook_path": "/hook"}, "stats": {"prometheus": {"enabled": true, "token": "m", "path": "/hook"}}}`,
		"prometheus healthz collision":          `{"bot": {"token": "t", "mode": "webhook"}, "server": {"webhook_secret": "s"}, "stats": {"prometheus": {"enabled": true, "token": "m", "path": "/healthz"}}}`,
		"ai joke prompt template without %s":    `{"bot": {"token": "t"}, "anek": {"inline": {"ai_joke_prompt_template": "joke please"}}}`,
		"ai joke prompt template with two %s":   `{"bot": {"token": "t"}, "anek": {"inline": {"ai_joke_prompt_template": "%s and %s"}}}`,
		"ai joke prompt template with bad verb": `{"bot": {"token": "t"}, "anek": {"inline": {"ai_joke_prompt_template": "%s and %d"}}}`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := loadConfig([]string{"-config", writeConfig(t, content)}); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestLoadConfig_LLMSystemPrompt_Default(t *testing.T) {
	clearEnv(t)
	cfg, err := loadConfig([]string{"-config", writeConfig(t, `{"bot": {"token": "t"}}`)})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.llmSystemPrompt != anekbot.DefaultSystemPrompt {
		t.Errorf("expected default system prompt, got %q", cfg.llmSystemPrompt)
	}
	if cfg.aiJokePromptTemplate != anekbot.DefaultAIJokePromptTemplate {
		t.Errorf("expected default AI joke prompt template, got %q", cfg.aiJokePromptTemplate)
	}
}

func TestLoadConfig_LLMSystemPrompt_FromFile(t *testing.T) {
	clearEnv(t)
	content := `{"bot": {"token": "t"}, "llm": {"system_prompt": "be terse"},
		"anek": {"inline": {"ai_joke_prompt_template": "tell a joke about %s"}}}`
	cfg, err := loadConfig([]string{"-config", writeConfig(t, content)})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.llmSystemPrompt != "be terse" {
		t.Errorf("expected overridden system prompt, got %q", cfg.llmSystemPrompt)
	}
	if cfg.aiJokePromptTemplate != "tell a joke about %s" {
		t.Errorf("expected overridden AI joke prompt template, got %q", cfg.aiJokePromptTemplate)
	}
}

func TestLoadConfig_Prometheus(t *testing.T) {
	clearEnv(t)
	t.Setenv("WEBHOOK_SECRET_TOKEN", "s")
	path := writeConfig(t, `{"bot": {"token": "t"}, "stats": {"prometheus": {"enabled": false}}}`)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.prometheusEnabled {
		t.Error("stats.prometheus.enabled: false must disable the prometheus endpoint without requiring a token")
	}
}

func TestLoadConfig_PrometheusTokenFileOverridesEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("WEBHOOK_SECRET_TOKEN", "s")
	t.Setenv("METRICS_TOKEN", "env-token")
	path := writeConfig(t, `{"bot": {"token": "t"}, "stats": {"prometheus": {"token": "file-token"}}}`)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.prometheusToken != "file-token" {
		t.Errorf("prometheusToken = %q, want file value %q to win over env", cfg.prometheusToken, "file-token")
	}
	if cfg.prometheusPath != "/metrics" {
		t.Errorf("prometheusPath = %q, want default %q", cfg.prometheusPath, "/metrics")
	}
}

func TestLoadConfig_AdminUsernames(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `{"bot": {"token": "t"}, "admin": {"usernames": ["@Alice", "bob"]}}`)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	want := []string{"@Alice", "bob"}
	if len(cfg.adminUsernames) != len(want) || cfg.adminUsernames[0] != want[0] || cfg.adminUsernames[1] != want[1] {
		t.Errorf("adminUsernames = %v, want %v", cfg.adminUsernames, want)
	}
}

func TestLoadConfig_PersistenceFile_Default(t *testing.T) {
	clearEnv(t)
	cfg, err := loadConfig([]string{"-config", writeConfig(t, `{"bot": {"token": "t"}}`)})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.persistenceFile != "" {
		t.Errorf("persistenceFile = %q, want empty (persistence disabled) by default", cfg.persistenceFile)
	}
}

func TestLoadConfig_PersistenceFile_FromFile(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `{"bot": {"token": "t"}, "stats": {"persistence_file": "/var/lib/anekbot/stats.json"}}`)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.persistenceFile != "/var/lib/anekbot/stats.json" {
		t.Errorf("persistenceFile = %q, want %q", cfg.persistenceFile, "/var/lib/anekbot/stats.json")
	}
}

func TestLoadConfig_PersistenceFile_FromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("ANEKBOT_STATS_PERSISTENCE_FILE", "/tmp/stats.json")

	cfg, err := loadConfig([]string{"-config", writeConfig(t, `{"bot": {"token": "t"}}`)})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.persistenceFile != "/tmp/stats.json" {
		t.Errorf("persistenceFile = %q, want %q from env", cfg.persistenceFile, "/tmp/stats.json")
	}
}

func TestLoadConfig_PersistenceFile_FileOverridesEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("ANEKBOT_STATS_PERSISTENCE_FILE", "/tmp/env-stats.json")
	path := writeConfig(t, `{"bot": {"token": "t"}, "stats": {"persistence_file": "/tmp/file-stats.json"}}`)

	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.persistenceFile != "/tmp/file-stats.json" {
		t.Errorf("persistenceFile = %q, want the file value to win over env", cfg.persistenceFile)
	}
}
