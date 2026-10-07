package anekbot

import (
	"context"
	"regexp"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/ThreeHundredBugs/anekbot/internal/logging"
)

type HelpHandler struct {
	text           string
	commandPattern *regexp.Regexp
}

func NewHelpHandler(botUsername string, anekEnabled, swearingEnabled, llmEnabled bool) *HelpHandler {
	return &HelpHandler{
		text:           buildHelpText(botUsername, anekEnabled, swearingEnabled, llmEnabled),
		commandPattern: regexp.MustCompile(`(?i)^/(?:help|start)(?:@` + regexp.QuoteMeta(botUsername) + `)?(?:\s|$)`),
	}
}

func buildHelpText(botUsername string, anekEnabled, swearingEnabled, llmEnabled bool) string {
	var b strings.Builder
	b.WriteString("Вот что я умею:\n")

	if anekEnabled {
		b.WriteString("\n- Напишите «анек!» в сообщении или отправьте /анек — пришлю случайный анекдот.")
		b.WriteString("\n- Наберите @" + botUsername + " в любом чате (инлайн-режим), чтобы получить анекдот, не отправляя сообщение.")
	}
	if swearingEnabled {
		b.WriteString("\n- Реагирую 🤬 на сообщения с матом.")
	}
	if llmEnabled {
		b.WriteString("\n- Упомяните меня как @" + botUsername + " с вопросом — отвечу с помощью ИИ.")
	}
	if !anekEnabled && !swearingEnabled && !llmEnabled {
		b.WriteString("\nПока что все мои функции отключены.")
	}

	return b.String()
}

func (h *HelpHandler) Name() string {
	return "help"
}

func (h *HelpHandler) Handle(ctx context.Context, sender Sender, update *models.Update) {
	if update.Message == nil || update.Message.Text == "" {
		return
	}
	msg := update.Message

	if !h.commandPattern.MatchString(msg.Text) {
		return
	}
	logging.Debugf("help handler: replying to %s in chat_id=%d", msg.Text, msg.Chat.ID)

	if _, err := sender.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            h.text,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	}); err != nil {
		logging.Warnf("help handler: send message: %v", err)
	}
}
