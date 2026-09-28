package anekbot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-telegram/bot/models"
	"golang.org/x/text/encoding/charmap"

	"github.com/ThreeHundredBugs/anekbot/internal/llm"
)

func newTestAnekHandler(t *testing.T, body string, randValue float64) (h *AnekHandler, lastQuery func() url.Values) {
	t.Helper()

	win1251Body, err := charmap.Windows1251.NewEncoder().String(body)
	if err != nil {
		t.Fatalf("encode fixture as windows-1251: %v", err)
	}

	var mu sync.Mutex
	var captured url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		captured = r.URL.Query()
		mu.Unlock()
		w.Write([]byte(win1251Body))
	}))
	t.Cleanup(server.Close)

	h = &AnekHandler{
		client:         server.Client(),
		baseURL:        server.URL,
		randFloat:      func() float64 { return randValue },
		commandPattern: anekCommandPattern(""),

		inlineFetchDeadline: defaultInlineFetchDeadline,
		jokeAttemptTimeout:  defaultJokeAttemptTimeout,
	}
	lastQuery = func() url.Values {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
	return h, lastQuery
}

func TestAnekHandler_Trigger(t *testing.T) {
	wantJoke := `Штирлиц вошел в комнату и сказал: "привет"` + "\n" + `всем`
	fixture := `{"content":"` + wantJoke + `"}`

	h, query := newTestAnekHandler(t, fixture, 0.5) // below 0.85 -> normal joke
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   10,
		Chat: models.Chat{ID: 123},
		Text: "хочу анек! срочно",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Fatalf("expected 1 message sent, got %d", len(sender.sentMessages))
	}
	sent := sender.sentMessages[0]
	if sent.Text != wantJoke {
		t.Errorf("joke text = %q, want %q", sent.Text, wantJoke)
	}
	if sent.ChatID != int64(123) {
		t.Errorf("chat id = %v, want 123", sent.ChatID)
	}
	if sent.ReplyParameters == nil || sent.ReplyParameters.MessageID != 10 {
		t.Errorf("reply parameters = %+v, want reply to message 10", sent.ReplyParameters)
	}
	if got := query().Get("CType"); got != "1" {
		t.Errorf("CType = %q, want 1 for randFloat below 0.85", got)
	}
}

func TestAnekHandler_18PlusBranch(t *testing.T) {
	h, query := newTestAnekHandler(t, `{"content":"joke"}`, 0.9) // above 0.85 -> 18+ joke
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "анек!",
	}}

	h.Handle(context.Background(), sender, update)

	if got := query().Get("CType"); got != "11" {
		t.Errorf("CType = %q, want 11 for randFloat above 0.85", got)
	}
}

func TestAnekHandler_CaseInsensitiveSubstring(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "ну давай АНЕК! мне",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Fatalf("expected trigger to match case-insensitively, got %d messages", len(sender.sentMessages))
	}
}

func TestAnekHandler_SlashCommand(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "/анек",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Fatalf("expected /анек to trigger a joke like анек!, got %d messages", len(sender.sentMessages))
	}
}

func TestAnekHandler_SlashCommandCaseInsensitiveWithTrailingText(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "/АНЕК пожалуйста",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Errorf("expected the command to match case-insensitively with trailing text, got %d messages", len(sender.sentMessages))
	}
}

func TestAnekHandler_SlashCommandWithBotUsername(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	h.SetBotUsername("anekbot")
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "/анек@anekbot",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Fatalf("expected /анек@<botusername> to trigger a joke, got %d messages", len(sender.sentMessages))
	}
}

func TestAnekHandler_SlashCommandWrongBotIgnored(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	h.SetBotUsername("anekbot")
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "/анек@someotherbot",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 0 {
		t.Errorf("expected a command addressed to a different bot to be ignored, got %d messages", len(sender.sentMessages))
	}
}

func TestAnekHandler_SlashCommandPrefixDoesNotFalselyMatch(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "/анекдот",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 0 {
		t.Errorf("expected /анекдот not to match the /анек command, got %d messages", len(sender.sentMessages))
	}
}

func TestAnekHandler_NoTrigger(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "просто привет",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 0 {
		t.Errorf("expected no message sent without the trigger, got %d", len(sender.sentMessages))
	}
}

func TestAnekHandler_NoMessage(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	sender := &fakeSender{}

	h.Handle(context.Background(), sender, &models.Update{})

	if len(sender.sentMessages) != 0 {
		t.Errorf("expected no message sent when Message is nil, got %d", len(sender.sentMessages))
	}
}

func TestAnekHandler_HandleInline(t *testing.T) {
	wantJoke := "Штирлиц зашел в комнату\nи сказал что-то очень длинное и запутанное про партизан и явки"
	fixture := `{"content":"` + wantJoke + `"}`

	h, _ := newTestAnekHandler(t, fixture, 0.1)
	sender := &fakeSender{}

	update := &models.Update{InlineQuery: &models.InlineQuery{ID: "query-1", Query: ""}}

	h.HandleInline(context.Background(), sender, update)

	if len(sender.inlineAnswers) != 1 {
		t.Fatalf("expected 1 AnswerInlineQuery call, got %d", len(sender.inlineAnswers))
	}
	answer := sender.inlineAnswers[0]
	if answer.InlineQueryID != "query-1" {
		t.Errorf("inline query id = %q, want %q", answer.InlineQueryID, "query-1")
	}
	if len(answer.Results) != inlineSuggestionCount {
		t.Fatalf("expected %d results, got %d", inlineSuggestionCount, len(answer.Results))
	}

	seenIDs := make(map[string]bool)
	for _, result := range answer.Results {
		article, ok := result.(*models.InlineQueryResultArticle)
		if !ok {
			t.Fatalf("result type = %T, want *models.InlineQueryResultArticle", result)
		}
		if seenIDs[article.ID] {
			t.Errorf("duplicate result id %q", article.ID)
		}
		seenIDs[article.ID] = true

		content, ok := article.InputMessageContent.(models.InputTextMessageContent)
		if !ok {
			t.Fatalf("input message content type = %T, want models.InputTextMessageContent", article.InputMessageContent)
		}
		if content.MessageText != wantJoke {
			t.Errorf("message text = %q, want %q", content.MessageText, wantJoke)
		}
		if strings.Contains(article.Title, "\n") {
			t.Errorf("title = %q, should not contain newlines", article.Title)
		}
	}
}

func TestAnekHandler_HandleInline_NoInlineQuery(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	sender := &fakeSender{}

	h.HandleInline(context.Background(), sender, &models.Update{})

	if len(sender.inlineAnswers) != 0 {
		t.Errorf("expected no AnswerInlineQuery call when InlineQuery is nil, got %d", len(sender.inlineAnswers))
	}
}

func TestAnekHandler_HandleInline_WithQuery_ShowsPlaceholder(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	sender := &fakeSender{}

	update := &models.Update{InlineQuery: &models.InlineQuery{ID: "query-1", Query: "  про  котов  "}}

	h.HandleInline(context.Background(), sender, update)

	if len(sender.inlineAnswers) != 1 {
		t.Fatalf("expected 1 AnswerInlineQuery call, got %d", len(sender.inlineAnswers))
	}
	answer := sender.inlineAnswers[0]
	if answer.CacheTime != inlineAICacheSeconds {
		t.Errorf("cache time = %d, want %d", answer.CacheTime, inlineAICacheSeconds)
	}
	if len(answer.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(answer.Results))
	}

	article, ok := answer.Results[0].(*models.InlineQueryResultArticle)
	if !ok {
		t.Fatalf("result type = %T, want *models.InlineQueryResultArticle", answer.Results[0])
	}
	if article.ID != aiJokeResultID {
		t.Errorf("result id = %q, want %q", article.ID, aiJokeResultID)
	}

	wantTitle := "Сгенерировать ИИ-анек на тему про котов"
	if article.Title != wantTitle {
		t.Errorf("title = %q, want %q", article.Title, wantTitle)
	}

	// Placeholder only; the real joke arrives later via HandleChosenInlineResult.
	content, ok := article.InputMessageContent.(models.InputTextMessageContent)
	if !ok {
		t.Fatalf("input message content type = %T, want models.InputTextMessageContent", article.InputMessageContent)
	}
	if content.MessageText != aiJokeGeneratingMessage {
		t.Errorf("message text = %q, want placeholder %q", content.MessageText, aiJokeGeneratingMessage)
	}

	// A keyboard must be present; that's how Telegram assigns an inline_message_id.
	markup, ok := article.ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok {
		t.Fatalf("reply markup type = %T, want *models.InlineKeyboardMarkup", article.ReplyMarkup)
	}
	if len(markup.InlineKeyboard) != 1 || len(markup.InlineKeyboard[0]) != 1 {
		t.Fatalf("inline keyboard = %+v, want a single button", markup.InlineKeyboard)
	}
	if got := markup.InlineKeyboard[0][0].CallbackData; got != aiJokePendingCallbackData {
		t.Errorf("callback data = %q, want %q", got, aiJokePendingCallbackData)
	}
}

func TestAnekHandler_HandleChosenInlineResult_GeneratesAndEditsJoke(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	sender := &fakeSender{}
	h.SetLLM(NewLLM(llm.Limits{}, &fakeLLMProvider{answer: "смешной анекдот"}))

	update := &models.Update{ChosenInlineResult: &models.ChosenInlineResult{
		ResultID:        aiJokeResultID,
		Query:           "котов",
		InlineMessageID: "inline-msg-1",
	}}

	h.HandleChosenInlineResult(context.Background(), sender, update)

	if len(sender.editedMessages) != 1 {
		t.Fatalf("expected 1 EditMessageText call, got %d", len(sender.editedMessages))
	}
	edited := sender.editedMessages[0]
	if edited.InlineMessageID != "inline-msg-1" {
		t.Errorf("inline message id = %q, want %q", edited.InlineMessageID, "inline-msg-1")
	}
	if edited.Text != "смешной анекдот" {
		t.Errorf("text = %q, want %q", edited.Text, "смешной анекдот")
	}

	// The pending-button keyboard must be cleared once the real joke is in.
	markup, ok := edited.ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok {
		t.Fatalf("reply markup type = %T, want *models.InlineKeyboardMarkup", edited.ReplyMarkup)
	}
	if len(markup.InlineKeyboard) != 0 {
		t.Errorf("inline keyboard = %+v, want it cleared", markup.InlineKeyboard)
	}
}

func TestAnekHandler_HandleChosenInlineResult_UnavailableWhenLLMNil(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	sender := &fakeSender{}

	update := &models.Update{ChosenInlineResult: &models.ChosenInlineResult{
		ResultID:        aiJokeResultID,
		Query:           "котов",
		InlineMessageID: "inline-msg-1",
	}}

	h.HandleChosenInlineResult(context.Background(), sender, update)

	if len(sender.editedMessages) != 1 {
		t.Fatalf("expected 1 EditMessageText call, got %d", len(sender.editedMessages))
	}
	if got := sender.editedMessages[0].Text; got != llmUnavailableMessage {
		t.Errorf("text = %q, want %q", got, llmUnavailableMessage)
	}
}

func TestAnekHandler_HandleChosenInlineResult_UnavailableWhenProviderFails(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	sender := &fakeSender{}
	h.SetLLM(NewLLM(llm.Limits{}, &fakeLLMProvider{err: errors.New("down")}))

	update := &models.Update{ChosenInlineResult: &models.ChosenInlineResult{
		ResultID:        aiJokeResultID,
		Query:           "котов",
		InlineMessageID: "inline-msg-1",
	}}

	h.HandleChosenInlineResult(context.Background(), sender, update)

	if len(sender.editedMessages) != 1 {
		t.Fatalf("expected 1 EditMessageText call, got %d", len(sender.editedMessages))
	}
	if got := sender.editedMessages[0].Text; got != llmUnavailableMessage {
		t.Errorf("text = %q, want %q", got, llmUnavailableMessage)
	}
}

func TestAnekHandler_HandleChosenInlineResult_NoPromoOnError(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	h.SetPromotions(mustParsePromotions(t, testPromotionsJSON, 0.1, 0.5))
	sender := &fakeSender{}
	h.SetLLM(NewLLM(llm.Limits{}, &fakeLLMProvider{err: errors.New("down")}))

	h.HandleChosenInlineResult(context.Background(), sender, &models.Update{ChosenInlineResult: &models.ChosenInlineResult{
		ResultID: aiJokeResultID, Query: "cats", InlineMessageID: "m",
	}})

	if len(sender.editedMessages) != 1 {
		t.Fatalf("expected 1 EditMessageText call, got %d", len(sender.editedMessages))
	}
	markup, ok := sender.editedMessages[0].ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok {
		t.Fatalf("reply markup type = %T, want *models.InlineKeyboardMarkup", sender.editedMessages[0].ReplyMarkup)
	}
	if len(markup.InlineKeyboard) != 0 {
		t.Errorf("keyboard = %+v, want empty (no promo on an errored message)", markup.InlineKeyboard)
	}
}

func TestAnekHandler_HandleChosenInlineResult_RateLimitedMessage(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	sender := &fakeSender{}
	h.SetLLM(NewLLM(llm.Limits{PerUserLimit: 1}, &fakeLLMProvider{answer: "joke"}))

	update := &models.Update{ChosenInlineResult: &models.ChosenInlineResult{
		ResultID:        aiJokeResultID,
		Query:           "котов",
		InlineMessageID: "inline-msg-1",
	}}
	h.HandleChosenInlineResult(context.Background(), sender, update)
	h.HandleChosenInlineResult(context.Background(), sender, update)

	if len(sender.editedMessages) != 2 {
		t.Fatalf("expected 2 EditMessageText calls, got %d", len(sender.editedMessages))
	}
	if got := sender.editedMessages[1].Text; got != llmRateLimitedMessage {
		t.Errorf("text = %q, want %q (second call should be rate-limited, not generically unavailable)", got, llmRateLimitedMessage)
	}
}

func TestAnekHandler_HandleChosenInlineResult_IgnoresOtherResults(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	sender := &fakeSender{}
	h.SetLLM(NewLLM(llm.Limits{}, &fakeLLMProvider{answer: "смешной анекдот"}))

	update := &models.Update{ChosenInlineResult: &models.ChosenInlineResult{
		ResultID:        "0", // one of the random-joke results, not the AI one
		InlineMessageID: "inline-msg-1",
	}}

	h.HandleChosenInlineResult(context.Background(), sender, update)

	if len(sender.editedMessages) != 0 {
		t.Errorf("expected no EditMessageText call for a non-AI result, got %d", len(sender.editedMessages))
	}
}

func TestAnekHandler_HandleChosenInlineResult_NoUpdate(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	sender := &fakeSender{}

	h.HandleChosenInlineResult(context.Background(), sender, &models.Update{})

	if len(sender.editedMessages) != 0 {
		t.Errorf("expected no EditMessageText call when ChosenInlineResult is nil, got %d", len(sender.editedMessages))
	}
}

func TestAnekHandler_HandleCallback_AcksPendingButton(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	sender := &fakeSender{}

	update := &models.Update{CallbackQuery: &models.CallbackQuery{ID: "cb-1", Data: aiJokePendingCallbackData}}

	h.HandleCallback(context.Background(), sender, update)

	if len(sender.callbackAnswers) != 1 || sender.callbackAnswers[0].CallbackQueryID != "cb-1" {
		t.Fatalf("expected AnswerCallbackQuery for cb-1, got %+v", sender.callbackAnswers)
	}
}

func TestAnekHandler_HandleCallback_IgnoresUnrelatedCallback(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	sender := &fakeSender{}

	update := &models.Update{CallbackQuery: &models.CallbackQuery{ID: "cb-1", Data: "some_other_action"}}

	h.HandleCallback(context.Background(), sender, update)

	if len(sender.callbackAnswers) != 0 {
		t.Errorf("expected an unrelated callback to be ignored, got %d answers", len(sender.callbackAnswers))
	}
}

func TestAnekHandler_HandleCallback_NoCallbackQuery(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	sender := &fakeSender{}

	h.HandleCallback(context.Background(), sender, &models.Update{})

	if len(sender.callbackAnswers) != 0 {
		t.Errorf("expected no calls when CallbackQuery is nil, got %d", len(sender.callbackAnswers))
	}
}

func TestInlineTitle_TruncatesLongJokes(t *testing.T) {
	joke := strings.Repeat("а", inlineTitleMaxRunes+20)

	title := inlineTitle(joke)

	if got := []rune(title); len(got) != inlineTitleMaxRunes+1 { // +1 for the trailing "…"
		t.Errorf("title length = %d runes, want %d", len(got), inlineTitleMaxRunes+1)
	}
	if !strings.HasSuffix(title, "…") {
		t.Errorf("title = %q, want it truncated with a trailing ellipsis", title)
	}
}

func TestAnekHandler_SetInline_DisabledIgnoresInlineQueries(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	h.SetInline(false, true)
	sender := &fakeSender{}

	h.HandleInline(context.Background(), sender, &models.Update{InlineQuery: &models.InlineQuery{ID: "q"}})

	if len(sender.inlineAnswers) != 0 {
		t.Errorf("expected no answer with inline disabled, got %d", len(sender.inlineAnswers))
	}
}

func TestAnekHandler_HandleInline_RetriesFailedFetchThenSucceeds(t *testing.T) {
	wantJoke := "joke"
	fixture := `{"content":"` + wantJoke + `"}`
	win1251Body, err := charmap.Windows1251.NewEncoder().String(fixture)
	if err != nil {
		t.Fatalf("encode fixture as windows-1251: %v", err)
	}

	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&requestCount, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(win1251Body))
	}))
	t.Cleanup(server.Close)

	h := &AnekHandler{
		client:              server.Client(),
		baseURL:             server.URL,
		randFloat:           func() float64 { return 0.1 },
		commandPattern:      anekCommandPattern(""),
		inlineFetchDeadline: defaultInlineFetchDeadline,
		jokeAttemptTimeout:  defaultJokeAttemptTimeout,
	}
	sender := &fakeSender{}

	h.HandleInline(context.Background(), sender, &models.Update{InlineQuery: &models.InlineQuery{ID: "q"}})

	if len(sender.inlineAnswers) != 1 || len(sender.inlineAnswers[0].Results) != inlineSuggestionCount {
		t.Fatalf("expected the failed fetch to be retried and all %d results returned, got %+v", inlineSuggestionCount, sender.inlineAnswers)
	}
	if got := atomic.LoadInt32(&requestCount); got <= inlineSuggestionCount {
		t.Errorf("expected a retry beyond the initial %d requests, got %d requests total", inlineSuggestionCount, got)
	}
}

func TestAnekHandler_HandleInline_ShowsFewerThanThreeWhenSomeFetchesFail(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	h.client = &http.Client{Timeout: h.client.Timeout, Transport: alwaysFailTransport{}}
	sender := &fakeSender{}

	h.HandleInline(context.Background(), sender, &models.Update{InlineQuery: &models.InlineQuery{ID: "q"}})

	if len(sender.inlineAnswers) != 1 {
		t.Fatalf("expected 1 AnswerInlineQuery call even when every fetch fails, got %d", len(sender.inlineAnswers))
	}
	if got := len(sender.inlineAnswers[0].Results); got != 0 {
		t.Errorf("expected 0 results when every fetch fails, got %d", got)
	}
}

type alwaysFailTransport struct{}

func (alwaysFailTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("simulated network failure")
}

func TestAnekHandler_SetInline_AIJokesDisabledFallsBackToRegularJokes(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	h.SetInline(true, false)
	sender := &fakeSender{}

	h.HandleInline(context.Background(), sender, &models.Update{InlineQuery: &models.InlineQuery{ID: "q", Query: "cats"}})

	if len(sender.inlineAnswers) != 1 || len(sender.inlineAnswers[0].Results) != inlineSuggestionCount {
		t.Fatalf("expected regular joke suggestions, got %+v", sender.inlineAnswers)
	}

	h.SetLLM(NewLLM(llm.Limits{}, &fakeLLMProvider{answer: "x"}))
	h.HandleChosenInlineResult(context.Background(), sender, &models.Update{ChosenInlineResult: &models.ChosenInlineResult{
		ResultID: aiJokeResultID, Query: "cats", InlineMessageID: "m",
	}})
	if len(sender.editedMessages) != 0 {
		t.Error("AI joke must not be generated when ai_jokes is disabled")
	}
}
