package anekbot

import (
	"context"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/ThreeHundredBugs/anekbot/internal/llm"
	"github.com/ThreeHundredBugs/anekbot/internal/stats"
)

func newTestDispatcher(t *testing.T) *Dispatcher {
	t.Helper()
	anek, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	swearing, err := NewSwearingHandler("")
	if err != nil {
		t.Fatalf("NewSwearingHandler: %v", err)
	}
	return NewDispatcher(anek, swearing, nil, nil)
}

func TestDispatch_Message_DoesNotAnswerInlineQuery(t *testing.T) {
	d := newTestDispatcher(t)
	sender := &fakeSender{}
	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "нам всем пиздец",
	}}

	d.Dispatch(context.Background(), sender, update)

	if len(sender.inlineAnswers) != 0 {
		t.Errorf("expected a message update to never call AnswerInlineQuery, got %d calls", len(sender.inlineAnswers))
	}
	if len(sender.reactions) != 1 {
		t.Errorf("expected the swearing handler to still run for a message update, got %d reactions", len(sender.reactions))
	}
}

func TestDispatch_Message_RunsLLMHandlerWhenConfigured(t *testing.T) {
	anek, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	swearing, err := NewSwearingHandler("")
	if err != nil {
		t.Fatalf("NewSwearingHandler: %v", err)
	}
	questions := NewQuestionsHandler("anekbot", NewLLM("", llm.Limits{}, &fakeLLMProvider{answer: "42"}))
	d := NewDispatcher(anek, swearing, questions, nil)

	sender := &fakeSender{}
	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "@anekbot what's up",
	}}

	d.Dispatch(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Errorf("expected the llm handler to send 1 message, got %d", len(sender.sentMessages))
	}
}

func TestDispatch_Message_RunsLLMAndSwearingHandlers(t *testing.T) {
	anek, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	swearing, err := NewSwearingHandler("")
	if err != nil {
		t.Fatalf("NewSwearingHandler: %v", err)
	}
	questions := NewQuestionsHandler("anekbot", NewLLM("", llm.Limits{}, &fakeLLMProvider{answer: "42"}))
	d := NewDispatcher(anek, swearing, questions, nil)

	sender := &fakeSender{}
	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "@anekbot хуйло",
	}}

	d.Dispatch(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Errorf("expected the llm handler to send 1 message, got %d", len(sender.sentMessages))
	}
	if len(sender.reactions) != 1 {
		t.Errorf("expected the swearing handler to react once, got %d reactions", len(sender.reactions))
	}
}

func TestDispatch_Message_SkipsDisabledAnekAndSwearing(t *testing.T) {
	d := NewDispatcher(nil, nil, nil, nil)
	sender := &fakeSender{}
	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "анек! нам всем пиздец",
	}}

	d.Dispatch(context.Background(), sender, update)

	if len(sender.sentMessages) != 0 {
		t.Errorf("expected a disabled anek handler to send no messages, got %d", len(sender.sentMessages))
	}
	if len(sender.reactions) != 0 {
		t.Errorf("expected a disabled swearing handler to set no reactions, got %d", len(sender.reactions))
	}
}

func TestDispatch_Message_RunsHelpHandlerWhenConfigured(t *testing.T) {
	anek, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	swearing, err := NewSwearingHandler("")
	if err != nil {
		t.Fatalf("NewSwearingHandler: %v", err)
	}
	help := NewHelpHandler("anekbot", true, true, true)
	d := NewDispatcher(anek, swearing, nil, help)

	sender := &fakeSender{}
	update := &models.Update{Message: &models.Message{
		ID:   1,
		Chat: models.Chat{ID: 1},
		Text: "/help",
	}}

	d.Dispatch(context.Background(), sender, update)

	if len(sender.sentMessages) != 1 {
		t.Errorf("expected the help handler to send 1 message, got %d", len(sender.sentMessages))
	}
}

func TestDispatch_InlineQuery_SkipsDisabledAnek(t *testing.T) {
	d := NewDispatcher(nil, nil, nil, nil)
	sender := &fakeSender{}
	update := &models.Update{InlineQuery: &models.InlineQuery{ID: "q1"}}

	d.Dispatch(context.Background(), sender, update)

	if len(sender.inlineAnswers) != 0 {
		t.Errorf("expected a disabled anek handler to answer no inline queries, got %d", len(sender.inlineAnswers))
	}
}

func TestDispatch_InlineQuery_WithQuery_ShowsPlaceholderWithoutCallingLLM(t *testing.T) {
	anek, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	primary := &fakeLLMProvider{answer: "42"}
	anek.SetLLM(NewLLM("", llm.Limits{}, primary))
	d := NewDispatcher(anek, nil, nil, nil)

	sender := &fakeSender{}
	update := &models.Update{InlineQuery: &models.InlineQuery{ID: "q1", Query: "котов"}}

	d.Dispatch(context.Background(), sender, update)

	if len(sender.inlineAnswers) != 1 {
		t.Fatalf("expected 1 AnswerInlineQuery call, got %d", len(sender.inlineAnswers))
	}
	if primary.calls != 0 {
		t.Errorf("expected the LLM to not be called while answering the inline query, got %d calls", primary.calls)
	}
	article := sender.inlineAnswers[0].Results[0].(*models.InlineQueryResultArticle)
	content := article.InputMessageContent.(models.InputTextMessageContent)
	if content.MessageText != aiJokeGeneratingMessage {
		t.Errorf("message text = %q, want placeholder %q", content.MessageText, aiJokeGeneratingMessage)
	}
}

func TestDispatch_ChosenInlineResult_RunsAnekHandlerWithLLM(t *testing.T) {
	anek, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	anek.SetLLM(NewLLM("", llm.Limits{}, &fakeLLMProvider{answer: "42"}))
	d := NewDispatcher(anek, nil, nil, nil)

	sender := &fakeSender{}
	update := &models.Update{ChosenInlineResult: &models.ChosenInlineResult{
		ResultID:        aiJokeResultID,
		Query:           "котов",
		InlineMessageID: "inline-msg-1",
	}}

	d.Dispatch(context.Background(), sender, update)

	if len(sender.editedMessages) != 1 {
		t.Fatalf("expected the anek handler to edit 1 message, got %d", len(sender.editedMessages))
	}
	if got := sender.editedMessages[0].Text; got != "42" {
		t.Errorf("edited text = %q, want %q", got, "42")
	}
}

func TestDispatch_ChosenInlineResult_SkipsDisabledAnek(t *testing.T) {
	d := NewDispatcher(nil, nil, nil, nil)
	sender := &fakeSender{}
	update := &models.Update{ChosenInlineResult: &models.ChosenInlineResult{ResultID: aiJokeResultID}}

	d.Dispatch(context.Background(), sender, update)

	if len(sender.editedMessages) != 0 {
		t.Errorf("expected a disabled anek handler to make no calls, got %d", len(sender.editedMessages))
	}
}

func TestDispatch_CallbackQuery_RunsAnekHandler(t *testing.T) {
	anek, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	d := NewDispatcher(anek, nil, nil, nil)

	sender := &fakeSender{}
	update := &models.Update{CallbackQuery: &models.CallbackQuery{ID: "cb-1", Data: aiJokePendingCallbackData}}

	d.Dispatch(context.Background(), sender, update)

	if len(sender.callbackAnswers) != 1 || sender.callbackAnswers[0].CallbackQueryID != "cb-1" {
		t.Fatalf("expected the anek handler to answer the callback query, got %+v", sender.callbackAnswers)
	}
}

func TestDispatch_CallbackQuery_RunsStatsHandler(t *testing.T) {
	d := NewDispatcher(nil, nil, nil, nil)
	d.SetStatsHandler(NewStatsHandler(stats.New(), []string{"admin"}))

	sender := &fakeSender{}
	update := &models.Update{CallbackQuery: &models.CallbackQuery{
		ID:   "cb-1",
		Data: statsRefreshCallbackData,
		From: models.User{ID: 42, Username: "admin"},
		Message: models.MaybeInaccessibleMessage{
			Message: &models.Message{ID: 100, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}},
		},
	}}

	d.Dispatch(context.Background(), sender, update)

	if len(sender.callbackAnswers) != 1 || sender.callbackAnswers[0].CallbackQueryID != "cb-1" {
		t.Fatalf("expected the stats handler to answer the callback query, got %+v", sender.callbackAnswers)
	}
	if len(sender.editedMessages) != 1 {
		t.Errorf("expected the stats handler to refresh the message, got %d edits", len(sender.editedMessages))
	}
}

func TestDispatch_CallbackQuery_SkipsDisabledAnek(t *testing.T) {
	d := NewDispatcher(nil, nil, nil, nil)
	sender := &fakeSender{}
	update := &models.Update{CallbackQuery: &models.CallbackQuery{ID: "cb-1", Data: aiJokePendingCallbackData}}

	d.Dispatch(context.Background(), sender, update)

	if len(sender.callbackAnswers) != 0 {
		t.Errorf("expected a disabled anek handler to make no calls, got %d", len(sender.callbackAnswers))
	}
}

func TestDispatch_InlineQuery_DoesNotRunMessageHandlers(t *testing.T) {
	d := newTestDispatcher(t)
	sender := &fakeSender{}
	update := &models.Update{InlineQuery: &models.InlineQuery{ID: "q1"}}

	d.Dispatch(context.Background(), sender, update)

	if len(sender.sentMessages) != 0 {
		t.Errorf("expected an inline query update to never call SendMessage, got %d calls", len(sender.sentMessages))
	}
	if len(sender.reactions) != 0 {
		t.Errorf("expected an inline query update to never call SetMessageReaction, got %d calls", len(sender.reactions))
	}
}
