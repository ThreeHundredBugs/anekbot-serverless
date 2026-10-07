package anekbot

import (
	"context"
	"regexp"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/ThreeHundredBugs/anekbot/internal/llm"
	"github.com/ThreeHundredBugs/anekbot/internal/logging"
	"github.com/ThreeHundredBugs/anekbot/internal/stats"
)

type QuestionsHandler struct {
	llm            *llm.LLM
	stats          *stats.Stats
	mentionPattern *regexp.Regexp
}

func NewQuestionsHandler(botUsername string, client *llm.LLM) *QuestionsHandler {
	return &QuestionsHandler{
		llm:            client,
		mentionPattern: regexp.MustCompile(`(?i)@` + regexp.QuoteMeta(botUsername) + `\b`),
	}
}

func (h *QuestionsHandler) SetStats(s *stats.Stats) {
	h.stats = s
}

func (h *QuestionsHandler) Name() string {
	return "questions"
}

func (h *QuestionsHandler) Handle(ctx context.Context, sender Sender, update *models.Update) {
	if update.Message == nil || update.Message.Text == "" {
		return
	}
	msg := update.Message

	question, ok := h.extractQuestion(msg.Text)
	if !ok {
		return
	}
	logging.Debugf("questions handler: answering question in chat_id=%d", msg.Chat.ID)

	answer, err := h.llm.AskFor(ctx, userID(msg.From), question)
	if err != nil {
		if _, sendErr := sender.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            llmErrorMessage(err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		}); sendErr != nil {
			logging.Warnf("questions handler: send unavailable message: %v", sendErr)
		}
		return
	}

	h.stats.RecordQuestionAnswered(stats.UserID(userID(msg.From)), username(msg.From))

	answer = truncateToRunes(answer, telegramMessageMaxRunes)

	_, err = sender.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            answer,
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
	if err == nil {
		return
	}
	logging.Warnf("questions handler: send message: %v", err)

	// The model's HTML may be malformed; fall back to plain text rather than dropping the answer.
	if _, err := sender.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            answer,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	}); err != nil {
		logging.Warnf("questions handler: send plain-text fallback: %v", err)
	}
}

func (h *QuestionsHandler) extractQuestion(text string) (question string, ok bool) {
	if strings.HasPrefix(text, "/") {
		// message is command, don't answer it, if it's like /command@mybot
		return "", false
	}

	loc := h.mentionPattern.FindStringIndex(text)
	if loc == nil {
		return "", false
	}

	question = strings.TrimSpace(text[:loc[0]] + text[loc[1]:])
	if question == "" {
		return "", false
	}
	return question, true
}
