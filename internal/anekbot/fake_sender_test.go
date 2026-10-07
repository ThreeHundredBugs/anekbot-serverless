package anekbot

import (
	"context"
	"errors"
	"sync"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// fakeSender records calls instead of talking to Telegram
type fakeSender struct {
	mu              sync.Mutex
	sentMessages    []*bot.SendMessageParams
	reactions       []*bot.SetMessageReactionParams
	inlineAnswers   []*bot.AnswerInlineQueryParams
	editedMessages  []*bot.EditMessageTextParams
	callbackAnswers []*bot.AnswerCallbackQueryParams

	// failSendMessageIf makes SendMessage fail (without recording) when it returns true.
	failSendMessageIf func(*bot.SendMessageParams) bool

	// failEditMessageTextIf works like failSendMessageIf, but for EditMessageText.
	failEditMessageTextIf func(*bot.EditMessageTextParams) bool
	// failEditMessageTextErr overrides the error failEditMessageTextIf causes, so tests can
	// simulate a specific Telegram API error message rather than a generic failure.
	failEditMessageTextErr error
}

func (f *fakeSender) SendMessage(_ context.Context, params *bot.SendMessageParams) (*models.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSendMessageIf != nil && f.failSendMessageIf(params) {
		return nil, errors.New("fakeSender: simulated SendMessage failure")
	}
	f.sentMessages = append(f.sentMessages, params)
	return &models.Message{}, nil
}

func (f *fakeSender) SetMessageReaction(_ context.Context, params *bot.SetMessageReactionParams) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reactions = append(f.reactions, params)
	return true, nil
}

func (f *fakeSender) AnswerInlineQuery(_ context.Context, params *bot.AnswerInlineQueryParams) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inlineAnswers = append(f.inlineAnswers, params)
	return true, nil
}

func (f *fakeSender) EditMessageText(_ context.Context, params *bot.EditMessageTextParams) (*models.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failEditMessageTextIf != nil && f.failEditMessageTextIf(params) {
		if f.failEditMessageTextErr != nil {
			return nil, f.failEditMessageTextErr
		}
		return nil, errors.New("fakeSender: simulated EditMessageText failure")
	}
	f.editedMessages = append(f.editedMessages, params)
	return &models.Message{}, nil
}

func (f *fakeSender) AnswerCallbackQuery(_ context.Context, params *bot.AnswerCallbackQueryParams) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callbackAnswers = append(f.callbackAnswers, params)
	return true, nil
}
