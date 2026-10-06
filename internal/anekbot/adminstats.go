package anekbot

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/ThreeHundredBugs/anekbot/internal/logging"
	"github.com/ThreeHundredBugs/anekbot/internal/stats"
	"github.com/ThreeHundredBugs/anekbot/internal/version"
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

	statsText := formatStats(h.stats.Snapshot(statsTopUsers))
	if _, err := sender.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      msg.Chat.ID,
		Text:        statsText,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: statsKeyboard(),
	}); err != nil {
		logging.Warnf("stats handler: send message: %v", err)
		if _, err := sender.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:      msg.Chat.ID,
			Text:        statsText,
			ReplyMarkup: statsKeyboard(),
		}); err != nil {
			logging.Warnf("stats handler: send plain-text fallback: %v", err)
		}
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

	statsText := formatStats(h.stats.Snapshot(statsTopUsers))
	if _, err := sender.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:      msg.Chat.ID,
		MessageID:   msg.ID,
		Text:        statsText,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: statsKeyboard(),
	}); err != nil {
		if isMessageNotModified(err) {
			// Stats haven't changed since the message was last shown; Telegram rejects a
			// no-op edit outright. Nothing to do: the currently displayed text is already
			// correct, so falling back to a plain-text re-edit would only replace it with
			// unformatted text for no reason.
			logging.Debugf("stats handler: refresh is a no-op, stats unchanged")
			return
		}
		logging.Warnf("stats handler: edit message text: %v", err)
		if _, err := sender.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:      msg.Chat.ID,
			MessageID:   msg.ID,
			Text:        statsText,
			ReplyMarkup: statsKeyboard(),
		}); err != nil {
			logging.Warnf("stats handler: edit message text plain-text fallback: %v", err)
		}
	}
}

func isMessageNotModified(err error) bool {
	return strings.Contains(err.Error(), "message is not modified")
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

	b.WriteString(renderTable(
		nil,
		[][]string{
			{"Версия", version.Version},
			{"Коммит", version.Commit()},
			{"Аптайм", formatUptime(snap.Uptime)},
			{"Всего пользователей", fmt.Sprint(snap.TotalUsers)},
			{"Анеков всего", fmt.Sprint(snap.TotalAneks)},
			{"из них ИИ", fmt.Sprint(snap.TotalAIAneks)},
			{"из них классических", fmt.Sprint(snap.TotalAneks - snap.TotalAIAneks)},
			{"Вопросов к ИИ", fmt.Sprint(snap.QuestionsAnswered)},
			{"Реакций на мат", fmt.Sprint(snap.SwearingReactions)},
			{"Промо показано", fmt.Sprint(snap.PromotionsShown)},
			{"Запросов к ИИ: OK", fmt.Sprint(snap.LLMRequestsOK)},
			{"Запросов к ИИ: ошибка", fmt.Sprint(snap.LLMRequestsError)},
			{"Fallback сработал", fmt.Sprint(snap.LLMFallbacks)},
			{"Выполняется сейчас", fmt.Sprint(snap.LLMConcurrencyInUse)},
			{"Отказано: лимит юзера", fmt.Sprint(snap.RateLimitRejectionsPerUser)},
			{"Отказано: лимит параллелизма", fmt.Sprint(snap.RateLimitRejectionsConcurrency)},
		},
	))
	b.WriteString("\n\n")
	b.WriteString(formatLLMProviderTable(snap.LLMProviders))
	b.WriteString("\n\n")
	b.WriteString(formatTopUsers(snap.TopUsers))

	return b.String()
}

func formatLLMProviderTable(providers []stats.LLMProviderStats) string {
	rows := make([][]string, 0, len(providers)+1)
	var successTotal, failTotal, grandTotal int64
	for _, p := range providers {
		rows = append(rows, []string{
			p.Provider,
			fmt.Sprint(p.SuccessPrimary),
			fmt.Sprint(p.SuccessFallback),
			fmt.Sprint(p.FailPrimary),
			fmt.Sprint(p.FailFallback),
			fmt.Sprint(p.Total()),
		})
		successTotal += p.SuccessPrimary + p.SuccessFallback
		failTotal += p.FailPrimary + p.FailFallback
		grandTotal += p.Total()
	}
	rows = append(rows, []string{"Итого", fmt.Sprint(successTotal), "-", fmt.Sprint(failTotal), "-", fmt.Sprint(grandTotal)})

	return renderTable([]string{"Провайдер", "OK(прям.)", "OK(fallback)", "Fail(прям.)", "Fail(fallback)", "Всего"}, rows)
}

// formatUptime renders d as the largest two non-zero units, e.g. "3d 2h", "5h 12m", "42s".
func formatUptime(d time.Duration) string {
	d = d.Round(time.Second)
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	d -= hours * time.Hour
	minutes := d / time.Minute
	d -= minutes * time.Minute
	seconds := d / time.Second

	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}

func formatTopUsers(top []stats.UserTotal) string {
	if len(top) == 0 {
		return "Топ пользователей: пока нет данных."
	}

	var b strings.Builder
	b.WriteString("Топ пользователей:")
	for i, u := range top {
		name := "id:" + fmt.Sprint(u.UserID)
		if u.Username != "" {
			name = "@" + escapeHTML(u.Username)
		}
		fmt.Fprintf(&b, "\n%d. %s — %d", i+1, name, u.Count)
	}
	return b.String()
}

// renderTable renders header and rows as a fixed-width, space-aligned table inside an HTML
// <pre> block, which Telegram shows in a monospace font so the columns actually line up. A
// nil/empty header omits the header row entirely (column widths then come from rows alone).
// Column widths are computed from the unescaped cell text so escaping (which only ever makes
// text longer) can't throw off alignment.
func renderTable(header []string, rows [][]string) string {
	cols := len(header)
	if cols == 0 && len(rows) > 0 {
		cols = len(rows[0])
	}
	widths := make([]int, cols)
	for i, h := range header {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if n := utf8.RuneCountInString(cell); n > widths[i] {
				widths[i] = n
			}
		}
	}

	var b strings.Builder
	b.WriteString("<pre>\n")
	writeRow := func(cells []string) {
		for i, cell := range cells {
			if i > 0 {
				b.WriteString("  ")
			}
			pad := widths[i] - utf8.RuneCountInString(cell)
			b.WriteString(escapeHTML(cell))
			b.WriteString(strings.Repeat(" ", pad))
		}
		b.WriteString("\n")
	}
	if len(header) > 0 {
		writeRow(header)
	}
	for _, row := range rows {
		writeRow(row)
	}
	b.WriteString("</pre>")
	return b.String()
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// escapeHTML escapes text inserted into a ParseModeHTML message; Telegram still parses
// entities inside <pre>/<code>, so they aren't exempt either.
func escapeHTML(text string) string {
	return htmlEscaper.Replace(text)
}
