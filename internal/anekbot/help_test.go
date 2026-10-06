package anekbot

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestHelpHandler_Trigger(t *testing.T) {
	h := NewHelpHandler("anekbot", true, true, true)
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   5,
		Chat: models.Chat{ID: 7},
		Text: "/help",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Fatalf("expected 1 message sent, got %d", len(sender.sentMessages))
	}
	sent := sender.sentMessages[0]
	if sent.ChatID != int64(7) {
		t.Errorf("chat id = %v, want 7", sent.ChatID)
	}
	if sent.ReplyParameters == nil || sent.ReplyParameters.MessageID != 5 {
		t.Errorf("reply parameters = %+v, want reply to message 5", sent.ReplyParameters)
	}
}

func TestHelpHandler_TriggerWithBotUsernameSuffix(t *testing.T) {
	h := NewHelpHandler("anekbot", true, true, true)
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "/help@anekbot",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Errorf("expected /help@<botusername> to trigger, got %d messages", len(sender.sentMessages))
	}
}

func TestHelpHandler_TriggersOnStart(t *testing.T) {
	h := NewHelpHandler("anekbot", true, true, true)
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "/start",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Errorf("expected /start to trigger the help text, got %d messages", len(sender.sentMessages))
	}
}

func TestHelpHandler_IgnoresUnrelatedCommand(t *testing.T) {
	h := NewHelpHandler("anekbot", true, true, true)
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "/help_me_please",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 0 {
		t.Errorf("expected /help_me_please to not trigger the help command, got %d messages", len(sender.sentMessages))
	}
}

func TestHelpHandler_IgnoresPlainText(t *testing.T) {
	h := NewHelpHandler("anekbot", true, true, true)
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "help",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 0 {
		t.Errorf("expected plain text without a leading slash to not trigger, got %d messages", len(sender.sentMessages))
	}
}

func TestHelpHandler_NoMessage(t *testing.T) {
	h := NewHelpHandler("anekbot", true, true, true)
	sender := &fakeSender{}

	h.Handle(context.Background(), sender, &models.Update{})

	if len(sender.sentMessages) != 0 {
		t.Errorf("expected no message sent when Message is nil, got %d", len(sender.sentMessages))
	}
}

func TestBuildHelpText_MentionsOnlyEnabledFeatures(t *testing.T) {
	text := buildHelpText("anekbot", true, false, false)

	if !strings.Contains(text, "анек!") {
		t.Errorf("expected help text to mention the anek trigger, got %q", text)
	}
	if strings.Contains(text, "матом") {
		t.Errorf("expected help text to not mention swearing when disabled, got %q", text)
	}
	if strings.Contains(text, "ИИ") {
		t.Errorf("expected help text to not mention the LLM feature when disabled, got %q", text)
	}
}

func TestBuildHelpText_AllDisabled(t *testing.T) {
	text := buildHelpText("anekbot", false, false, false)

	if !strings.Contains(text, "отключены") {
		t.Errorf("expected help text to say every feature is disabled, got %q", text)
	}
}
