package anekbot

import (
	"errors"

	"github.com/ThreeHundredBugs/anekbot/internal/llm"
)

const (
	telegramMessageMaxRunes = 4096

	llmUnavailableMessage = "ИИ сейчас недоступен, попробуйте ещё раз позже."
	llmRateLimitedMessage = "Слишком много запросов, попробуйте через минуту."
)

func llmErrorMessage(err error) string {
	if errors.Is(err, llm.ErrBusy) {
		return llmRateLimitedMessage
	}
	return llmUnavailableMessage
}

const DefaultSystemPrompt = "You are a helpful assistant replying in a Telegram chat. Keep answers concise. " +
	"This is a one-shot reply: the user cannot follow up or continue the conversation, so make your answer " +
	"self-contained and don't ask clarifying questions or offer to elaborate further. " +
	"Reply in Russian by default, unless the user's message is clearly written in another language, in which " +
	"case reply in that language instead. Don't format the message unless needed. " +
	"If you need to format, reply as Telegram HTML: only <b>, <i>, <u>, <s>, <code>, <pre> and <a href=\"...\"> tags are " +
	"supported, no other tags or Markdown syntax. Escape any literal <, > and & that aren't part of a tag."

func NewLLM(systemPrompt string, limits llm.Limits, providers ...llm.Provider) *llm.LLM {
	if systemPrompt == "" {
		systemPrompt = DefaultSystemPrompt
	}
	return llm.New(systemPrompt, limits, providers...)
}

func truncateToRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
