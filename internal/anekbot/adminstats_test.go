package anekbot

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/ThreeHundredBugs/anekbot/internal/stats"
)

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
	if !strings.Contains(sender.sentMessages[0].Text, "Всего анеков: 1") {
		t.Errorf("text = %q, want it to include the total", sender.sentMessages[0].Text)
	}
}

func TestStatsHandler_RepliesToAdminOnStart(t *testing.T) {
	s := stats.New()
	s.RecordAnek(1, "alice", "message", "classic")
	h := NewStatsHandler(s, []string{"@Admin"})
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate},
		From: &models.User{ID: 42, Username: "admin"},
		Text: "/start",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Fatalf("expected 1 message sent, got %d", len(sender.sentMessages))
	}
	if !strings.Contains(sender.sentMessages[0].Text, "Всего анеков: 1") {
		t.Errorf("text = %q, want it to include the total", sender.sentMessages[0].Text)
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
	if !strings.Contains(edited.Text, "Всего анеков: 1") {
		t.Errorf("text = %q, want refreshed total", edited.Text)
	}
	if _, ok := edited.ReplyMarkup.(*models.InlineKeyboardMarkup); !ok {
		t.Errorf("reply markup type = %T, want the refresh button kept", edited.ReplyMarkup)
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
	for _, want := range []string{
		"Вопросы к ИИ: 301", "Реакции на мат: 402", "Промо показано: 503",
		"604 успешно, 705 с ошибкой", "Fallback-провайдер сработал: 806",
		"Сейчас выполняется: 907", "Отказано по лимиту: 1008 на пользователя, 1109 по параллелизму",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text = %q, want it to contain %q", text, want)
		}
	}
}
