package anekbot

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/ThreeHundredBugs/anekbot/internal/logging"
	"github.com/ThreeHundredBugs/anekbot/internal/stats"
)

const (
	statsCommand  = "/stats"
	statsTopUsers = 10

	statsRefreshButtonText   = "🔄 Обновить"
	statsRefreshCallbackData = "anekbot_stats_refresh"
)

type StatsHandler struct {
	stats  *stats.Stats
	admins *Admins
}

func NewStatsHandler(s *stats.Stats, adminUsernames []string) *StatsHandler {
	return &StatsHandler{stats: s, admins: NewAdmins(adminUsernames)}
}

func (h *StatsHandler) Name() string {
	return "stats"
}

func (h *StatsHandler) Handle(ctx context.Context, sender Sender, update *models.Update) {
	if update.Message == nil {
		return
	}
	text := strings.TrimSpace(update.Message.Text)
	if text != statsCommand {
		return
	}
	msg := update.Message

	// chat.ID == from.ID confirms this is truly a private chat with the sender, not a
	// spoofed chat type on a forwarded/group message.
	if msg.Chat.Type != models.ChatTypePrivate || msg.From == nil || msg.Chat.ID != msg.From.ID {
		return
	}
	if !h.admins.IsAdmin(msg.From.Username) {
		return
	}

	logging.Debugf("stats handler: replying to %s for admin @%s", text, msg.From.Username)

	if _, err := sender.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      msg.Chat.ID,
		Text:        formatStats(h.stats.Snapshot(statsTopUsers)),
		ReplyMarkup: statsKeyboard(),
	}); err != nil {
		logging.Warnf("stats handler: send message: %v", err)
	}
}

// HandleCallback refreshes the stats message in place when the admin presses the
// keyboard button, so they never have to retype /stats to see current numbers.
func (h *StatsHandler) HandleCallback(ctx context.Context, sender Sender, update *models.Update) {
	if update.CallbackQuery == nil || update.CallbackQuery.Data != statsRefreshCallbackData {
		return
	}
	cb := update.CallbackQuery

	msg := cb.Message.Message
	if msg == nil || msg.Chat.Type != models.ChatTypePrivate || msg.Chat.ID != cb.From.ID {
		return
	}
	if !h.admins.IsAdmin(cb.From.Username) {
		return
	}

	if _, err := sender.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: cb.ID,
	}); err != nil {
		logging.Warnf("stats handler: answer callback query: %v", err)
	}

	logging.Debugf("stats handler: refreshing stats for admin @%s", cb.From.Username)

	if _, err := sender.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:      msg.Chat.ID,
		MessageID:   msg.ID,
		Text:        formatStats(h.stats.Snapshot(statsTopUsers)),
		ReplyMarkup: statsKeyboard(),
	}); err != nil {
		logging.Warnf("stats handler: edit message text: %v", err)
	}
}

func statsKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: statsRefreshButtonText, CallbackData: statsRefreshCallbackData}},
		},
	}
}

func formatStats(snap stats.Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Всего анеков: %d\nИИ-анеков: %d\nПользователей: %d\n", snap.TotalAneks, snap.TotalAIAneks, snap.TotalUsers)

	fmt.Fprintf(&b, "\nВопросы к ИИ: %d\nРеакции на мат: %d\nПромо показано: %d\n",
		snap.QuestionsAnswered, snap.SwearingReactions, snap.PromotionsShown)

	fmt.Fprintf(&b, "\nЗапросы к ИИ: %d успешно, %d с ошибкой\nFallback-провайдер сработал: %d раз\nСейчас выполняется: %d\nОтказано по лимиту: %d на пользователя, %d по параллелизму\n",
		snap.LLMRequestsOK, snap.LLMRequestsError, snap.LLMFallbacks, snap.LLMConcurrencyInUse,
		snap.RateLimitRejectionsPerUser, snap.RateLimitRejectionsConcurrency)

	if len(snap.TopUsers) == 0 {
		b.WriteString("\nТоп пользователей: пока нет данных.")
		return b.String()
	}

	b.WriteString("\nТоп пользователей:")
	for i, u := range snap.TopUsers {
		name := "id:" + fmt.Sprint(u.UserID)
		if u.Username != "" {
			name = "@" + u.Username
		}
		fmt.Fprintf(&b, "\n%d. %s — %d", i+1, name, u.Count)
	}
	return b.String()
}
