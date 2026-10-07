package anekbot

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/ThreeHundredBugs/anekbot/internal/stats"
)

// rowMatches reports whether text has a line with label followed by value, regardless of the
// exact column padding renderTable uses.
func rowMatches(t *testing.T, text, label, value string) {
	t.Helper()
	pattern := regexp.QuoteMeta(label) + `\s+` + regexp.QuoteMeta(value) + `\b`
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Errorf("text = %q, want a row matching %q", text, pattern)
	}
}

func TestStatsHandler_RepliesToAdminInPrivateChat(t *testing.T) {
	s := stats.New()
	s.RecordAnek(1, "alice", "message", "classic")
	h := NewStatsHandler(s, []string{"@Admin"})
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate},
		From: &models.User{ID: 42, Username: "admin"},
		Text: "/stats",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Fatalf("expected 1 message sent, got %d", len(sender.sentMessages))
	}
	rowMatches(t, sender.sentMessages[0].Text, "Анеков всего", "1")
}

func TestStatsHandler_IgnoresStart(t *testing.T) {
	h := NewStatsHandler(stats.New(), []string{"@Admin"})
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate},
		From: &models.User{ID: 42, Username: "admin"},
		Text: "/start",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 0 {
		t.Errorf("expected /start to be left for the help handler, got %d messages", len(sender.sentMessages))
	}
}

func TestStatsHandler_AttachesRefreshButtonForAdminOnly(t *testing.T) {
	h := NewStatsHandler(stats.New(), []string{"admin"})
	sender := &fakeSender{}

	h.Handle(context.Background(), sender, &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
		From: &models.User{ID: 7, Username: "admin"},
		Text: "/stats",
	}})

	if len(sender.sentMessages) != 1 {
		t.Fatalf("expected 1 message sent, got %d", len(sender.sentMessages))
	}
	markup, ok := sender.sentMessages[0].ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok {
		t.Fatalf("reply markup type = %T, want *models.InlineKeyboardMarkup", sender.sentMessages[0].ReplyMarkup)
	}
	if len(markup.InlineKeyboard) != 1 || len(markup.InlineKeyboard[0]) != 1 ||
		markup.InlineKeyboard[0][0].CallbackData != statsRefreshCallbackData {
		t.Errorf("keyboard = %+v, want a single refresh button", markup.InlineKeyboard)
	}

	// A non-admin never even gets a message, so they can never see this button.
	sender2 := &fakeSender{}
	h.Handle(context.Background(), sender2, &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 9, Type: models.ChatTypePrivate},
		From: &models.User{ID: 9, Username: "rando"},
		Text: "/stats",
	}})
	if len(sender2.sentMessages) != 0 {
		t.Errorf("expected no message (and no button) for a non-admin, got %d", len(sender2.sentMessages))
	}
}

func TestStatsHandler_HandleCallback_RefreshesInPlace(t *testing.T) {
	s := stats.New()
	h := NewStatsHandler(s, []string{"admin"})
	sender := &fakeSender{}

	update := &models.Update{CallbackQuery: &models.CallbackQuery{
		ID:   "cb-1",
		Data: statsRefreshCallbackData,
		From: models.User{ID: 42, Username: "admin"},
		Message: models.MaybeInaccessibleMessage{
			Message: &models.Message{ID: 100, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}},
		},
	}}

	s.RecordAnek(1, "alice", "message", "classic")
	h.HandleCallback(context.Background(), sender, update)

	if len(sender.callbackAnswers) != 1 || sender.callbackAnswers[0].CallbackQueryID != "cb-1" {
		t.Fatalf("expected AnswerCallbackQuery for cb-1, got %+v", sender.callbackAnswers)
	}
	if len(sender.editedMessages) != 1 {
		t.Fatalf("expected 1 EditMessageText call, got %d", len(sender.editedMessages))
	}
	edited := sender.editedMessages[0]
	if edited.ChatID != int64(42) || edited.MessageID != 100 {
		t.Errorf("chat id / message id = %v / %v, want 42 / 100", edited.ChatID, edited.MessageID)
	}
	rowMatches(t, edited.Text, "Анеков всего", "1")
	if _, ok := edited.ReplyMarkup.(*models.InlineKeyboardMarkup); !ok {
		t.Errorf("reply markup type = %T, want the refresh button kept", edited.ReplyMarkup)
	}
}

func TestStatsHandler_HandleCallback_NoopWhenStatsUnchanged(t *testing.T) {
	s := stats.New()
	h := NewStatsHandler(s, []string{"admin"})
	sender := &fakeSender{
		failEditMessageTextIf: func(p *bot.EditMessageTextParams) bool {
			return p.ParseMode == models.ParseModeHTML
		},
		failEditMessageTextErr: fmt.Errorf("%w, message is not modified", bot.ErrorBadRequest),
	}

	update := &models.Update{CallbackQuery: &models.CallbackQuery{
		ID:   "cb-1",
		Data: statsRefreshCallbackData,
		From: models.User{ID: 42, Username: "admin"},
		Message: models.MaybeInaccessibleMessage{
			Message: &models.Message{ID: 100, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}},
		},
	}}

	h.HandleCallback(context.Background(), sender, update)

	if len(sender.callbackAnswers) != 1 {
		t.Fatalf("expected the callback to still be answered, got %d", len(sender.callbackAnswers))
	}
	if len(sender.editedMessages) != 0 {
		t.Errorf("expected no plain-text fallback edit when stats are unchanged, got %d", len(sender.editedMessages))
	}
}

func TestStatsHandler_HandleCallback_IgnoresNonAdmin(t *testing.T) {
	h := NewStatsHandler(stats.New(), []string{"admin"})
	sender := &fakeSender{}

	update := &models.Update{CallbackQuery: &models.CallbackQuery{
		ID:   "cb-1",
		Data: statsRefreshCallbackData,
		From: models.User{ID: 9, Username: "rando"},
		Message: models.MaybeInaccessibleMessage{
			Message: &models.Message{ID: 100, Chat: models.Chat{ID: 9, Type: models.ChatTypePrivate}},
		},
	}}

	h.HandleCallback(context.Background(), sender, update)

	if len(sender.callbackAnswers) != 0 || len(sender.editedMessages) != 0 {
		t.Errorf("expected a non-admin callback to be ignored entirely, got answers=%d edits=%d",
			len(sender.callbackAnswers), len(sender.editedMessages))
	}
}

func TestStatsHandler_HandleCallback_IgnoresUnrelatedData(t *testing.T) {
	h := NewStatsHandler(stats.New(), []string{"admin"})
	sender := &fakeSender{}

	update := &models.Update{CallbackQuery: &models.CallbackQuery{
		ID:   "cb-1",
		Data: "some_other_action",
		From: models.User{ID: 42, Username: "admin"},
		Message: models.MaybeInaccessibleMessage{
			Message: &models.Message{ID: 100, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}},
		},
	}}

	h.HandleCallback(context.Background(), sender, update)

	if len(sender.callbackAnswers) != 0 || len(sender.editedMessages) != 0 {
		t.Errorf("expected unrelated callback data to be ignored, got answers=%d edits=%d",
			len(sender.callbackAnswers), len(sender.editedMessages))
	}
}

func TestStatsHandler_IgnoresNonAdmin(t *testing.T) {
	h := NewStatsHandler(stats.New(), []string{"admin"})
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
		From: &models.User{ID: 7, Username: "rando"},
		Text: "/stats",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 0 {
		t.Errorf("expected no reply for a non-admin, got %d messages", len(sender.sentMessages))
	}
}

func TestStatsHandler_IgnoresGroupChat(t *testing.T) {
	h := NewStatsHandler(stats.New(), []string{"admin"})
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: -100, Type: models.ChatTypeGroup},
		From: &models.User{ID: 7, Username: "admin"},
		Text: "/stats",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 0 {
		t.Errorf("expected /stats to be ignored outside a private chat, got %d messages", len(sender.sentMessages))
	}
}

func TestStatsHandler_IgnoresSpoofedPrivateChat(t *testing.T) {
	h := NewStatsHandler(stats.New(), []string{"admin"})
	sender := &fakeSender{}

	// chat.type claims private but chat.id != from.id: not an actual DM with the sender.
	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 999, Type: models.ChatTypePrivate},
		From: &models.User{ID: 7, Username: "admin"},
		Text: "/stats",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 0 {
		t.Errorf("expected a mismatched chat/from id to be ignored, got %d messages", len(sender.sentMessages))
	}
}

func TestStatsHandler_UsernameMatchIsCaseInsensitiveAndStripsAt(t *testing.T) {
	h := NewStatsHandler(stats.New(), []string{"@Admin"})
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
		From: &models.User{ID: 7, Username: "ADMIN"},
		Text: "/stats",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Errorf("expected case-insensitive username match, got %d messages", len(sender.sentMessages))
	}
}

func TestStatsHandler_IgnoresOtherText(t *testing.T) {
	h := NewStatsHandler(stats.New(), []string{"admin"})
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
		From: &models.User{ID: 7, Username: "admin"},
		Text: "/stats please",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 0 {
		t.Errorf("expected exact /stats match only, got %d messages", len(sender.sentMessages))
	}
}

func TestFormatStats_NoDataYet(t *testing.T) {
	text := formatStats(stats.Snapshot{})
	if !strings.Contains(text, "пока нет данных") {
		t.Errorf("text = %q, want a no-data message for an empty snapshot", text)
	}
}

func TestFormatStats_IncludesActivityAndLLMCounters(t *testing.T) {
	// Distinct, hard-to-collide values: each should appear exactly once, at its own field's
	// spot in the formatted text.
	text := formatStats(stats.Snapshot{
		QuestionsAnswered:              301,
		SwearingReactions:              402,
		PromotionsShown:                503,
		LLMRequestsOK:                  604,
		LLMRequestsError:               705,
		LLMFallbacks:                   806,
		LLMConcurrencyInUse:            907,
		RateLimitRejectionsPerUser:     1008,
		RateLimitRejectionsConcurrency: 1109,
	})
	rowMatches(t, text, "Вопросов к ИИ", "301")
	rowMatches(t, text, "Реакций на мат", "402")
	rowMatches(t, text, "Промо показано", "503")
	rowMatches(t, text, "Запросов к ИИ: OK", "604")
	rowMatches(t, text, "Запросов к ИИ: ошибка", "705")
	rowMatches(t, text, "Fallback сработал", "806")
	rowMatches(t, text, "Выполняется сейчас", "907")
	rowMatches(t, text, "Отказано: лимит юзера", "1008")
	rowMatches(t, text, "Отказано: лимит параллелизма", "1109")
}

func TestFormatStats_LLMProviderTableWithTotalsRow(t *testing.T) {
	text := formatStats(stats.Snapshot{
		LLMProviders: []stats.LLMProviderStats{
			{Provider: "gemini-primary", SuccessPrimary: 8, SuccessFallback: 1, FailPrimary: 2, FailFallback: 0},
			{Provider: "huggingface-primary", SuccessPrimary: 0, SuccessFallback: 3, FailPrimary: 0, FailFallback: 1},
		},
	})

	providerRowMatches(t, text, "gemini-primary", 8, 1, 2, 0, 11)
	providerRowMatches(t, text, "huggingface-primary", 0, 3, 0, 1, 4)
	// Total row: combined success (8+1+0+3=12), combined fail (2+0+0+1=3), grand total (15).
	if !regexp.MustCompile(`Итого\s+12\s+-\s+3\s+-\s+15\b`).MatchString(text) {
		t.Errorf("text = %q, want a totals row matching Итого 12 - 3 - 15", text)
	}
}

func providerRowMatches(t *testing.T, text, provider string, successPrimary, successFallback, failPrimary, failFallback, total int64) {
	t.Helper()
	pattern := regexp.QuoteMeta(provider) + fmt.Sprintf(`\s+%d\s+%d\s+%d\s+%d\s+%d\b`,
		successPrimary, successFallback, failPrimary, failFallback, total)
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Errorf("text = %q, want a row matching %q", text, pattern)
	}
}
