package anekbot

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/ThreeHundredBugs/anekbot/internal/llm"
)

// fakeLLMProvider is a canned LLMProvider used to test LLMHandler without real HTTP calls.
type fakeLLMProvider struct {
	name   string
	answer string
	err    error
	calls  int
}

func (f *fakeLLMProvider) Name() string {
	return f.name
}

func (f *fakeLLMProvider) Weight() int {
	return 1
}

func (f *fakeLLMProvider) Ask(_ context.Context, _, _ string) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.answer, nil
}

func TestLLMHandler_Trigger(t *testing.T) {
	primary := &fakeLLMProvider{name: "Fake", answer: "42"}
	h := NewQuestionsHandler("anekbot", NewLLM("", llm.Limits{}, primary))
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   5,
		Chat: models.Chat{ID: 7},
		Text: "@anekbot what is the answer to everything?",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Fatalf("expected 1 message sent, got %d", len(sender.sentMessages))
	}
	sent := sender.sentMessages[0]
	if want := "42"; sent.Text != want {
		t.Errorf("text = %q, want %q", sent.Text, want)
	}
	if sent.ChatID != int64(7) {
		t.Errorf("chat id = %v, want 7", sent.ChatID)
	}
	if sent.ReplyParameters == nil || sent.ReplyParameters.MessageID != 5 {
		t.Errorf("reply parameters = %+v, want reply to message 5", sent.ReplyParameters)
	}
	if sent.ParseMode != models.ParseModeHTML {
		t.Errorf("parse mode = %q, want %q", sent.ParseMode, models.ParseModeHTML)
	}
}

func TestLLMHandler_CaseInsensitiveMention(t *testing.T) {
	h := NewQuestionsHandler("anekbot", NewLLM("", llm.Limits{}, &fakeLLMProvider{answer: "fact"}))
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "@AnekBot tell me a fact",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Errorf("expected mention to match case-insensitively, got %d messages", len(sender.sentMessages))
	}
}

func TestLLMHandler_NoMention(t *testing.T) {
	h := NewQuestionsHandler("anekbot", NewLLM("", llm.Limits{}, &fakeLLMProvider{answer: "fact"}))
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "просто привет",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 0 {
		t.Errorf("expected no message sent without a mention, got %d", len(sender.sentMessages))
	}
}

func TestLLMHandler_IgnoresSlashCommandWithMention(t *testing.T) {
	h := NewQuestionsHandler("anekbot", NewLLM("", llm.Limits{}, &fakeLLMProvider{answer: "fact"}))
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "/анек@anekbot",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 0 {
		t.Errorf("expected a bot command like /анек@anekbot not to be treated as a question, got %d messages", len(sender.sentMessages))
	}
}

func TestLLMHandler_MentionWithoutQuestion(t *testing.T) {
	h := NewQuestionsHandler("anekbot", NewLLM("", llm.Limits{}, &fakeLLMProvider{answer: "fact"}))
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "@anekbot",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 0 {
		t.Errorf("expected no message sent for a bare mention with no question, got %d", len(sender.sentMessages))
	}
}

func TestLLMHandler_NoMessage(t *testing.T) {
	h := NewQuestionsHandler("anekbot", NewLLM("", llm.Limits{}, &fakeLLMProvider{answer: "fact"}))
	sender := &fakeSender{}

	h.Handle(context.Background(), sender, &models.Update{})

	if len(sender.sentMessages) != 0 {
		t.Errorf("expected no message sent when Message is nil, got %d", len(sender.sentMessages))
	}
}

func TestLLMHandler_FallsBackToSecondaryProviderOnPrimaryError(t *testing.T) {
	primary := &fakeLLMProvider{err: errors.New("primary down")}
	fallback := &fakeLLMProvider{name: "Fallback", answer: "42"}
	h := NewQuestionsHandler("anekbot", NewLLM("", llm.Limits{}, primary, fallback))
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "@anekbot are you there?",
	}}

	h.Handle(context.Background(), sender, update)

	if primary.calls != 1 {
		t.Errorf("primary calls = %d, want 1", primary.calls)
	}
	if fallback.calls != 1 {
		t.Errorf("fallback calls = %d, want 1", fallback.calls)
	}
	if len(sender.sentMessages) != 1 {
		t.Fatalf("expected 1 message sent from the fallback provider, got %d", len(sender.sentMessages))
	}
	if want := "42"; sender.sentMessages[0].Text != want {
		t.Errorf("text = %q, want %q", sender.sentMessages[0].Text, want)
	}
}

func TestLLMHandler_SendsUnavailableMessageWhenPrimaryFailsWithNoFallback(t *testing.T) {
	primary := &fakeLLMProvider{err: errors.New("primary down")}
	h := NewQuestionsHandler("anekbot", NewLLM("", llm.Limits{}, primary))
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "@anekbot are you there?",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Fatalf("expected 1 unavailability message sent, got %d", len(sender.sentMessages))
	}
	if sent := sender.sentMessages[0]; sent.Text != llmUnavailableMessage {
		t.Errorf("text = %q, want %q", sent.Text, llmUnavailableMessage)
	}
}

func TestLLMHandler_SendsRateLimitedMessageWhenOverQuota(t *testing.T) {
	h := NewQuestionsHandler("anekbot", NewLLM("", llm.Limits{PerUserLimit: 1}, &fakeLLMProvider{answer: "42"}))
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "@anekbot are you there?",
	}}
	h.Handle(context.Background(), sender, update)
	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 2 {
		t.Fatalf("expected 2 messages sent, got %d", len(sender.sentMessages))
	}
	if sent := sender.sentMessages[1]; sent.Text != llmRateLimitedMessage {
		t.Errorf("text = %q, want %q (second call should be rate-limited, not generically unavailable)", sent.Text, llmRateLimitedMessage)
	}
}

func TestLLMHandler_SendsUnavailableMessageWhenBothProvidersFail(t *testing.T) {
	primary := &fakeLLMProvider{err: errors.New("primary down")}
	fallback := &fakeLLMProvider{err: errors.New("fallback down")}
	h := NewQuestionsHandler("anekbot", NewLLM("", llm.Limits{}, primary, fallback))
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "@anekbot are you there?",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Fatalf("expected 1 unavailability message sent, got %d", len(sender.sentMessages))
	}
	if sent := sender.sentMessages[0]; sent.Text != llmUnavailableMessage {
		t.Errorf("text = %q, want %q", sent.Text, llmUnavailableMessage)
	}
}

func TestLLMHandler_FallsBackToPlainTextWhenHTMLRejected(t *testing.T) {
	h := NewQuestionsHandler("anekbot", NewLLM("", llm.Limits{}, &fakeLLMProvider{name: "Fake", answer: "<b>42</b>"}))
	sender := &fakeSender{
		failSendMessageIf: func(p *bot.SendMessageParams) bool {
			return p.ParseMode == models.ParseModeHTML
		},
	}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "@anekbot what is the answer?",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Fatalf("expected 1 message sent after falling back, got %d", len(sender.sentMessages))
	}
	sent := sender.sentMessages[0]
	if sent.ParseMode != "" {
		t.Errorf("fallback parse mode = %q, want empty (plain text)", sent.ParseMode)
	}
	if want := "<b>42</b>"; sent.Text != want {
		t.Errorf("fallback text = %q, want %q", sent.Text, want)
	}
}

func TestLLMHandler_TruncatesLongAnswer(t *testing.T) {
	longAnswer := strings.Repeat("a", telegramMessageMaxRunes+100)
	h := NewQuestionsHandler("anekbot", NewLLM("", llm.Limits{}, &fakeLLMProvider{name: "Fake", answer: longAnswer}))
	sender := &fakeSender{}

	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "@anekbot tell me a long story",
	}}

	h.Handle(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Fatalf("expected 1 message sent, got %d", len(sender.sentMessages))
	}
	if got := len([]rune(sender.sentMessages[0].Text)); got != telegramMessageMaxRunes {
		t.Errorf("sent text length = %d runes, want %d", got, telegramMessageMaxRunes)
	}
}
