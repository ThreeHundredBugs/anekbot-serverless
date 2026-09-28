package anekbot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"golang.org/x/text/encoding/charmap"

	"github.com/ThreeHundredBugs/anekbot/internal/llm"
	"github.com/ThreeHundredBugs/anekbot/internal/logging"
	"github.com/ThreeHundredBugs/anekbot/internal/stats"
)

const (
	anekTrigger    = "анек!"
	anekCommand    = "анек"
	defaultBaseURL = "http://rzhunemogu.ru"

	// http://rzhunemogu.ru/FAQ.aspx
	anekTypeNormal = 1
	anekType18Plus = 11

	anekMaxResponseBytes = 16 << 10

	inlineSuggestionCount = 3
	inlineTitleMaxRunes   = 60

	// Telegram rejects answers to inline queries older than ~10s with "query is too old".
	defaultInlineFetchDeadline = 7 * time.Second
	defaultJokeAttemptTimeout  = 3 * time.Second
	inlineFetchRetryDelay      = 200 * time.Millisecond
	inlineFetchMaxAttempts     = 3

	inlineAITitlePrefix  = "Сгенерировать ИИ-анек на тему "
	inlineAITextMaxRunes = 100
	inlineAICacheSeconds = 120
	aiJokeResultID       = "ai-joke"

	// This button exists only so Telegram assigns an inline_message_id we can edit later.
	aiJokePendingButtonText   = "⏳"
	aiJokePendingCallbackData = "ai-joke-pending"
	classicResultPromoSuffix  = ":promo"

	sayCommandPrefix = "/say"
	sayResultID      = "admin-say"
)

// Must contain exactly one %s, which is replaced with the requested topic.
const DefaultAIJokePromptTemplate = "Придумай короткий анекдот на русском языке на тему: %s. " +
	"Ответь только текстом анекдота, без вступлений, пояснений и кавычек."

const aiJokeGeneratingMessage = "Генерирую ИИ-анек, подождите немного…"

type AnekHandler struct {
	client         *http.Client
	baseURL        string
	randFloat      func() float64
	promos         *Promotions
	llm            *llm.LLM
	stats          *stats.Stats
	admins         *Admins
	commandPattern *regexp.Regexp

	inlineFetchDeadline time.Duration
	jokeAttemptTimeout  time.Duration

	aiJokePromptTemplate string

	inlineDisabled  bool
	aiJokesDisabled bool
}

func NewAnekHandler() *AnekHandler {
	return &AnekHandler{
		client:         &http.Client{Timeout: 10 * time.Second},
		baseURL:        defaultBaseURL,
		randFloat:      rand.Float64,
		commandPattern: anekCommandPattern(""),

		inlineFetchDeadline: defaultInlineFetchDeadline,
		jokeAttemptTimeout:  defaultJokeAttemptTimeout,

		aiJokePromptTemplate: DefaultAIJokePromptTemplate,
	}
}

// lets /анек@botUsername match too, alongside a bare /анек
func (h *AnekHandler) SetBotUsername(botUsername string) {
	h.commandPattern = anekCommandPattern(botUsername)
}

func anekCommandPattern(botUsername string) *regexp.Regexp {
	pattern := `(?i)^/` + anekCommand
	if botUsername != "" {
		pattern += `(?:@` + regexp.QuoteMeta(botUsername) + `)?`
	}
	pattern += `(?:\s|$)`
	return regexp.MustCompile(pattern)
}

func (h *AnekHandler) SetPromotions(p *Promotions) {
	h.promos = p
}

func (h *AnekHandler) SetLLM(client *llm.LLM) {
	h.llm = client
}

func (h *AnekHandler) SetStats(s *stats.Stats) {
	h.stats = s
}

func (h *AnekHandler) SetAdmins(a *Admins) {
	h.admins = a
}

func (h *AnekHandler) SetInline(enabled, aiJokes bool) {
	h.inlineDisabled = !enabled
	h.aiJokesDisabled = !aiJokes
}

// SetAIJokePromptTemplate overrides DefaultAIJokePromptTemplate; a no-op if tmpl is empty.
func (h *AnekHandler) SetAIJokePromptTemplate(tmpl string) {
	if tmpl != "" {
		h.aiJokePromptTemplate = tmpl
	}
}

func (h *AnekHandler) Name() string {
	return "anek"
}

func (h *AnekHandler) Handle(ctx context.Context, sender Sender, update *models.Update) {
	if update.Message == nil || update.Message.Text == "" {
		return
	}
	msg := update.Message

	if !strings.Contains(strings.ToLower(msg.Text), anekTrigger) && !h.commandPattern.MatchString(msg.Text) {
		return
	}
	logging.Debugf("anek handler: matched trigger in chat_id=%d", msg.Chat.ID)

	joke, err := h.fetchJoke(ctx)
	if err != nil {
		logging.Warnf("anek handler: fetch joke: %v", err)
		return
	}

	if _, err := sender.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            joke,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	}); err != nil {
		logging.Warnf("anek handler: send message: %v", err)
		return
	}
	h.stats.RecordAnek(stats.UserID(userID(msg.From)), username(msg.From), "message", "classic")
}

func (h *AnekHandler) HandleInline(ctx context.Context, sender Sender, update *models.Update) {
	if update.InlineQuery == nil || h.inlineDisabled {
		return
	}
	query := update.InlineQuery

	if text, ok := parseSayText(query.Query); ok && h.admins.IsAdmin(username(query.From)) {
		h.answerSayInline(ctx, sender, query, text)
		return
	}

	topic := strings.Join(strings.Fields(query.Query), " ")
	if topic != "" && !h.aiJokesDisabled {
		h.answerAIJokePlaceholderInline(ctx, sender, query, topic)
		return
	}

	fetchCtx, cancel := context.WithTimeout(ctx, h.inlineFetchDeadline)
	defer cancel()

	jokes := make([]string, inlineSuggestionCount)
	var wg sync.WaitGroup
	wg.Add(inlineSuggestionCount)
	for i := range jokes {
		go func(i int) {
			defer wg.Done()
			jokes[i] = h.fetchJokeWithRetry(fetchCtx)
		}(i)
	}
	wg.Wait()

	results := make([]models.InlineQueryResult, 0, inlineSuggestionCount)
	for i, joke := range jokes {
		if joke == "" {
			continue
		}
		// Telegram doesn't report which (if any) offered result
		// the user picks unless inline feedback is enabled
		markup := h.promos.Keyboard()
		resultID := strconv.Itoa(i)
		article := &models.InlineQueryResultArticle{
			ID:                  resultID,
			Title:               inlineTitle(joke),
			InputMessageContent: models.InputTextMessageContent{MessageText: joke},
		}
		if markup != nil {
			// A nil *InlineKeyboardMarkup assigned directly to the ReplyMarkup interface field
			// survives as a non-nil interface holding a nil pointer, which Telegram rejects as
			// reply_markup:null instead of an omitted field.
			article.ID += classicResultPromoSuffix
			article.ReplyMarkup = markup
		}
		results = append(results, article)
	}

	logging.Debugf("anek handler: answering inline query with %d results", len(results))
	if _, err := sender.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
		InlineQueryID: query.ID,
		Results:       results,
		CacheTime:     1, // 0 is indistinguishable from unset and gets dropped
	}); err != nil {
		logging.Warnf("anek handler: answer inline query: %v", err)
	}
}

// parseSayText extracts the text after an admin-only "/say <text>" inline query.
func parseSayText(query string) (string, bool) {
	trimmed := strings.TrimSpace(query)
	if !strings.HasPrefix(strings.ToLower(trimmed), sayCommandPrefix) {
		return "", false
	}
	text := strings.TrimSpace(trimmed[len(sayCommandPrefix):])
	return text, text != ""
}

// answerSayInline lets an admin post arbitrary text verbatim via inline mode, bypassing
// both the random-joke fetch and any LLM call.
func (h *AnekHandler) answerSayInline(ctx context.Context, sender Sender, query *models.InlineQuery, text string) {
	result := &models.InlineQueryResultArticle{
		ID:                  sayResultID,
		Title:               inlineTitle(text),
		InputMessageContent: models.InputTextMessageContent{MessageText: text},
	}

	logging.Debugf("anek handler: answering inline query with admin /say text for user=%s", username(query.From))
	if _, err := sender.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
		InlineQueryID: query.ID,
		Results:       []models.InlineQueryResult{result},
		IsPersonal:    true, // never let Telegram serve this to a non-admin from a shared cache
	}); err != nil {
		logging.Warnf("anek handler: answer inline query: %v", err)
	}
}

// answerAIJokePlaceholderInline sends placeholder text; HandleChosenInlineResult fills
// in the real joke once Telegram reports the user picked this result.
func (h *AnekHandler) answerAIJokePlaceholderInline(ctx context.Context, sender Sender, query *models.InlineQuery, topic string) {
	title := truncateToRunes(inlineAITitlePrefix+topic, inlineAITextMaxRunes)

	result := &models.InlineQueryResultArticle{
		ID:                  aiJokeResultID,
		Title:               title,
		InputMessageContent: models.InputTextMessageContent{MessageText: aiJokeGeneratingMessage},
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				{{Text: aiJokePendingButtonText, CallbackData: aiJokePendingCallbackData}},
			},
		},
	}

	logging.Debugf("anek handler: answering inline query with AI-generate placeholder for topic %q", topic)
	if _, err := sender.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
		InlineQueryID: query.ID,
		Results:       []models.InlineQueryResult{result},
		CacheTime:     inlineAICacheSeconds,
	}); err != nil {
		logging.Warnf("anek handler: answer inline query: %v", err)
	}
}

// HandleChosenInlineResult replaces answerAIJokePlaceholderInline's placeholder with
// the real joke. Requires inline feedback enabled via BotFather's /setinlinefeedback.
func (h *AnekHandler) HandleChosenInlineResult(ctx context.Context, sender Sender, update *models.Update) {
	if update.ChosenInlineResult == nil {
		return
	}
	chosen := update.ChosenInlineResult

	if chosen.ResultID == sayResultID {
		return
	}
	if chosen.ResultID != aiJokeResultID {
		// A classic (non-AI) inline joke was actually sent
		if strings.HasSuffix(chosen.ResultID, classicResultPromoSuffix) {
			h.stats.RecordPromotionShown()
		}
		h.stats.RecordAnek(stats.UserID(userID(&chosen.From)), username(&chosen.From), "inline", "classic")
		return
	}
	if h.aiJokesDisabled || chosen.InlineMessageID == "" {
		return
	}
	topic := strings.Join(strings.Fields(chosen.Query), " ")
	if topic == "" {
		return
	}

	if h.llm == nil {
		h.editInlineMessage(ctx, sender, chosen.InlineMessageID, llmUnavailableMessage, false, false)
		return
	}

	logging.Debugf("anek handler: generating AI joke for topic %q", topic)
	joke, _, err := h.llm.AskFor(ctx, llm.UserID(chosen.From.ID), fmt.Sprintf(h.aiJokePromptTemplate, topic))
	if err != nil {
		logging.Warnf("anek handler: generate AI joke: %v", err)
		h.editInlineMessage(ctx, sender, chosen.InlineMessageID, llmErrorMessage(err), false, false)
		return
	}

	h.stats.RecordAnek(stats.UserID(userID(&chosen.From)), username(&chosen.From), "inline", "ai")
	h.editInlineMessage(ctx, sender, chosen.InlineMessageID, truncateToRunes(joke, telegramMessageMaxRunes), true, true)
}

func (h *AnekHandler) editInlineMessage(ctx context.Context, sender Sender, inlineMessageID, text string, tryHTML, withPromo bool) {
	markup := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}}
	if withPromo {
		if promoMarkup := h.promos.Keyboard(); promoMarkup != nil {
			markup = promoMarkup
			h.stats.RecordPromotionShown()
		}
	}
	params := &bot.EditMessageTextParams{
		InlineMessageID: inlineMessageID,
		Text:            text,
		ReplyMarkup:     markup,
	}
	if tryHTML {
		params.ParseMode = models.ParseModeHTML
	}

	if _, err := sender.EditMessageText(ctx, params); err != nil {
		logging.Warnf("anek handler: edit message text: %v", err)
		if tryHTML {
			plain := *params
			plain.ParseMode = ""
			if _, err := sender.EditMessageText(ctx, &plain); err != nil {
				logging.Warnf("anek handler: edit message text plain-text fallback: %v", err)
			}
		}
	}
}

// HandleCallback just acks button taps so Telegram doesn't show a stuck spinner;
// the joke itself arrives separately via HandleChosenInlineResult.
func (h *AnekHandler) HandleCallback(ctx context.Context, sender Sender, update *models.Update) {
	if update.CallbackQuery == nil ||
		(update.CallbackQuery.Data != aiJokePendingCallbackData && update.CallbackQuery.Data != promotionCallbackData) {
		return
	}
	if _, err := sender.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	}); err != nil {
		logging.Warnf("anek handler: answer callback query: %v", err)
	}
}

func inlineTitle(joke string) string {
	preview := strings.Join(strings.Fields(joke), " ")
	runes := []rune(preview)
	if len(runes) <= inlineTitleMaxRunes {
		return preview
	}
	return string(runes[:inlineTitleMaxRunes]) + "…"
}

// fetchJokeWithRetry returns "" if every attempt failed or ctx expired.
func (h *AnekHandler) fetchJokeWithRetry(ctx context.Context) string {
	for attempt := 1; attempt <= inlineFetchMaxAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, h.jokeAttemptTimeout)
		joke, err := h.fetchJoke(attemptCtx)
		cancel()
		if err == nil {
			return joke
		}
		logging.Warnf("anek handler: fetch joke for inline query (attempt %d/%d): %v", attempt, inlineFetchMaxAttempts, err)

		if attempt == inlineFetchMaxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(inlineFetchRetryDelay):
		}
	}
	return ""
}

func (h *AnekHandler) fetchJoke(ctx context.Context) (string, error) {
	anekType := anekTypeNormal
	if h.randFloat() > 0.85 {
		anekType = anekType18Plus
	}

	url := fmt.Sprintf("%s/RandJSON.aspx?CType=%d", h.baseURL, anekType)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, anekMaxResponseBytes))
	if err != nil {
		return "", err
	}

	// rzhunemogu.ru serves windows-1251; Telegram requires UTF-8.
	utf8Body, err := charmap.Windows1251.NewDecoder().Bytes(body)
	if err != nil {
		return "", fmt.Errorf("decode windows-1251 response: %w", err)
	}

	// The response isn't valid JSON
	joke := strings.TrimPrefix(string(utf8Body), `{"content":"`)
	joke = strings.TrimSuffix(joke, `"}`)
	if strings.TrimSpace(joke) == "" {
		return "", errors.New("empty joke")
	}
	return truncateToRunes(joke, telegramMessageMaxRunes), nil
}
