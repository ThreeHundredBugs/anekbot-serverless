# AGENTS.md

Guidance for agents working in this repository.

## What this is

`anekbot` is a Telegram bot (Go) that posts random jokes ("aneks") pulled from
rzhunemogu.ru, generates AI jokes on a topic via an LLM, answers free-form questions,
reacts to swearing, and supports inline mode. It runs in poll or webhook mode and
optionally exposes Prometheus metrics and an admin-only `/stats` panel.

## Layout

- `cmd/anekbot/` — entrypoint (`main.go`) and config loading (`config.go`). Config is a
  JSON file (see `config.example.json`) layered under env vars layered under flag
  defaults; file values win over env, env wins over hardcoded defaults
  (`fileEnvDefault`/`or` in `config.go`).
- `internal/anekbot/` — the bot's own logic: one `Handler` per feature (`AnekHandler`,
  `SwearingHandler`, `QuestionsHandler`, `HelpHandler`, `StatsHandler`), fanned out by
  `Dispatcher.Dispatch` in `dispatcher.go`. A `nil` handler field on `Dispatcher` means
  that feature is disabled — check for `nil`, don't assume every handler is wired.
  Handlers talk to Telegram through the `Sender` interface (`dispatcher.go`), which
  `fake_sender_test.go` implements for tests instead of hitting the real Bot API.
- `internal/llm/` — LLM provider implementations (Gemini, HuggingFace) behind a common
  `Provider` interface (`Name`, `Weight`, `Ask` — every provider carries its own weight);
  `internal/anekbot/llm.go` wraps them with per-user/global rate limiting
  (`internal/flowcontrol/`). `LLM.Ask` tries providers in an order built fresh per request by
  `Algorithm` (`RoundRobin`, `Order` fixed-list, `Random`; see `algorithm.go`). `RoundRobin` is
  the `Algorithm` zero value — both what `New` gets if `SetAlgorithm` is never called, and
  `ParseAlgorithm`'s default for an omitted/empty config value; write `"order"` explicitly to
  opt into the old fixed-list behavior. `cmd/anekbot/config.go` is the only place a weight of
  0/unset gets defaulted to 1 (production providers always reach `llm.go` with a real weight);
  `fakeProvider` in `llm_test.go` mirrors that same defaulting so tests can omit the field.
  Name and weight are plain constructor args on each provider (`NewGeminiProvider`/
  `NewHuggingFaceProvider`), set once at construction. `cmd/anekbot/config.go`'s
  `providerConfig` picks the backend by which of `gemini`/`huggingface` is set (not a `type`
  string), and every entry needs a unique `name`.
- `internal/stats/` — in-memory counters (`stats.go`, backed by
  `github.com/VictoriaMetrics/metrics` for Prometheus export plus a few `atomic.Int64`
  fields for the totals `/stats` needs) and file persistence (`persist.go`).
- `internal/logging/` — leveled logging (trace/debug/warn/…), configured once at startup
  from `LOG_LEVEL`/`bot.log_level`.

## Build, test, lint

CI (`.github/workflows/anekbot-go-ci.yaml`) runs, in this order — run the same before
calling anything done:

```sh
gofmt -l .          # must print nothing
go build ./...
go vet ./...
go test ./... -race
```

Always run `gofmt -w` (not just default `go build`) before finishing — CI fails on
unformatted files, and there's no separate lint step to catch it.

Tests are plain `go test`, no external services: HTTP-dependent code (joke fetching, LLM
calls) is tested against `httptest.Server`, and Telegram calls go through `fakeSender`
(`internal/anekbot/fake_sender_test.go`), which records calls instead of making them.

## Config

`cmd/anekbot/config.go`'s `fileConfig` struct is the source of truth for the JSON shape;
keep it and `config.example.json` in sync when adding a field. `loadFileConfig` uses
`json.Decoder.DisallowUnknownFields()`, so a typo'd or stale key in a config file is a
hard error, not a silent no-op — that's intentional, don't relax it.

Every JSON key should have a same-named Go field on `config` (e.g. `stats.prometheus.path`
→ `cfg.prometheusPath`, not `cfg.metricsPath`) — don't let the JSON shape and the Go
naming drift apart.

## Telegram API quirk to remember

`models.ReplyMarkup` (and similar `any`-typed fields with `omitempty`) does **not**
omit a typed-nil pointer: assigning a nil `*models.InlineKeyboardMarkup` straight into
such a field serializes as `"reply_markup":null`, which Telegram rejects outright. Only
set the field when the concrete value is non-nil; leave it unset (zero value) otherwise.
See `internal/anekbot/anek.go`'s classic inline-result loop for the pattern.

## Code style

- Minimal comments: don't add a comment that just restates what a short function, type,
  or line already shows in its name/signature. Keep the ones that carry information the
  code can't: a protocol/encoding quirk (e.g. rzhunemogu.ru serving windows-1251), an
  invariant the type system doesn't enforce ("ctx must carry a deadline"), a magic
  number's meaning, or behavior that spans multiple functions and isn't visible from a
  single glance. When you do keep one, make it one line stating the fact directly. This
  includes sentinel errors (`var ErrFoo = errors.New("...")`): if the message already
  states the fact, don't add a doc comment above it that just repeats the message.
- Errors are wrapped with `%w` (`fmt.Errorf("...: %w", err)`) throughout. Inside
  `internal/anekbot`, failures that shouldn't abort a handler are logged via
  `internal/logging` (`logging.Warnf`/`Debugf`) rather than returned; `internal/stats`
  and `internal/llm` just return errors and let the caller decide. `cmd/anekbot/main.go`
  uses the standard `log` package for startup/shutdown, since `internal/logging` isn't
  configured yet that early.
- Feature flags default to enabled (`enabled(*bool)` treats `nil` as `true`) except where
  a feature exposes something outward-facing by default risk (Prometheus endpoint,
  stats persistence) — those default to disabled/empty and require explicit opt-in.
