package anekbot

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/ThreeHundredBugs/anekbot/internal/llm"
	"github.com/ThreeHundredBugs/anekbot/internal/stats"
)

const testPromotionsJSON = `{"promotions": {"frequency": 0.2, "items": [
	{"message": "heavy", "weight": 3, "link": "https://example.com"},
	{"message": "light", "weight": 1}
]}}`

func mustParsePromotions(t *testing.T, data string, rolls ...float64) *Promotions {
	t.Helper()
	p, err := ParsePromotions([]byte(data))
	if err != nil {
		t.Fatalf("ParsePromotions: %v", err)
	}
	i := 0
	p.randFloat = func() float64 {
		v := rolls[i%len(rolls)]
		i++
		return v
	}
	return p
}

func TestParsePromotions_Invalid(t *testing.T) {
	tests := map[string]string{
		"bad json":           `{`,
		"frequency too high": `{"promotions": {"frequency": 1.5, "items": []}}`,
		"frequency negative": `{"promotions": {"frequency": -0.1, "items": []}}`,
		"empty message":      `{"promotions": {"frequency": 0.5, "items": [{"message": "", "weight": 1}]}}`,
		"zero weight":        `{"promotions": {"frequency": 0.5, "items": [{"message": "a", "weight": 0}]}}`,
		"bad link":           `{"promotions": {"frequency": 0.5, "items": [{"message": "a", "weight": 1, "link": "nope"}]}}`,
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParsePromotions([]byte(data)); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestPromotions_Pick(t *testing.T) {
	// First roll decides whether to show (< 0.2), second picks by weight (total 4).
	got, ok := mustParsePromotions(t, testPromotionsJSON, 0.1, 0.5).Pick()
	if !ok || got.Message != "heavy" {
		t.Errorf("got %+v, %v; want heavy", got, ok)
	}
	got, ok = mustParsePromotions(t, testPromotionsJSON, 0.1, 0.9).Pick()
	if !ok || got.Message != "light" {
		t.Errorf("got %+v, %v; want light", got, ok)
	}
	if _, ok := mustParsePromotions(t, testPromotionsJSON, 0.2, 0.5).Pick(); ok {
		t.Error("roll at frequency must not show a promotion")
	}
}

func TestPromotions_NilAndEmpty(t *testing.T) {
	var p *Promotions
	if p.Keyboard() != nil {
		t.Error("nil promotions must yield no keyboard")
	}
	empty := mustParsePromotions(t, `{"promotions": {"frequency": 1, "items": []}}`, 0)
	if empty.Keyboard() != nil {
		t.Error("empty promotions must yield no keyboard")
	}
}

func TestPromotions_KeyboardButtons(t *testing.T) {
	link := mustParsePromotions(t, testPromotionsJSON, 0.1, 0.5).Keyboard().InlineKeyboard[0][0]
	if link.Text != "heavy" || link.URL != "https://example.com" || link.CallbackData != "" {
		t.Errorf("link button = %+v", link)
	}
	plain := mustParsePromotions(t, testPromotionsJSON, 0.1, 0.9).Keyboard().InlineKeyboard[0][0]
	if plain.Text != "light" || plain.URL != "" || plain.CallbackData != promotionCallbackData {
		t.Errorf("callback button = %+v", plain)
	}
}

func TestAnekHandler_HandleInline_AttachesPromotion(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	h.SetPromotions(mustParsePromotions(t, testPromotionsJSON, 0.1, 0.5))
	sender := &fakeSender{}

	h.HandleInline(context.Background(), sender, &models.Update{InlineQuery: &models.InlineQuery{ID: "q"}})

	for _, r := range sender.inlineAnswers[0].Results {
		markup, ok := r.(*models.InlineQueryResultArticle).ReplyMarkup.(*models.InlineKeyboardMarkup)
		if !ok || markup.InlineKeyboard[0][0].Text != "heavy" {
			t.Errorf("reply markup = %+v, want heavy promotion", r.(*models.InlineQueryResultArticle).ReplyMarkup)
		}
	}
}

func TestAnekHandler_HandleChosenInlineResult_AttachesPromotion(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	h.SetPromotions(mustParsePromotions(t, testPromotionsJSON, 0.1, 0.5))
	sender := &fakeSender{}
	h.SetLLM(NewLLM("", llm.Limits{}, &fakeLLMProvider{answer: "joke"}))

	h.HandleChosenInlineResult(context.Background(), sender, &models.Update{ChosenInlineResult: &models.ChosenInlineResult{
		ResultID: aiJokeResultID, Query: "cats", InlineMessageID: "m",
	}})

	markup := sender.editedMessages[0].ReplyMarkup.(*models.InlineKeyboardMarkup)
	if len(markup.InlineKeyboard) != 1 || markup.InlineKeyboard[0][0].Text != "heavy" {
		t.Errorf("keyboard = %+v, want heavy promotion", markup.InlineKeyboard)
	}
}

// A classic inline query offers inlineSuggestionCount candidate results, each independently
// rolled for a promo; only one (if any) is ever actually picked and delivered. The shown-promo
// counter must reflect deliveries, not offers.
func TestAnekHandler_HandleInline_PromotionCountedOnlyOnConfirmedPick(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	h.SetPromotions(mustParsePromotions(t, testPromotionsJSON, 0.1, 0.5)) // always attaches a promo
	st := stats.New()
	h.SetStats(st)
	sender := &fakeSender{}

	h.HandleInline(context.Background(), sender, &models.Update{InlineQuery: &models.InlineQuery{ID: "q", Query: ""}})

	results := sender.inlineAnswers[0].Results
	if got := len(results); got != inlineSuggestionCount {
		t.Fatalf("expected %d offered results, got %d", inlineSuggestionCount, got)
	}
	if got := st.Snapshot(0).PromotionsShown; got != 0 {
		t.Errorf("promotions shown before any pick = %d, want 0", got)
	}

	// Every offered result should carry a promo with these rolls; each one's ID says so.
	for _, r := range results {
		id := r.(*models.InlineQueryResultArticle).ID
		if !strings.HasSuffix(id, classicResultPromoSuffix) {
			t.Fatalf("result id = %q, want it to carry the promo suffix", id)
		}
	}
	firstID := results[0].(*models.InlineQueryResultArticle).ID

	h.HandleChosenInlineResult(context.Background(), sender, &models.Update{ChosenInlineResult: &models.ChosenInlineResult{
		ResultID: firstID,
	}})

	if got := st.Snapshot(0).PromotionsShown; got != 1 {
		t.Errorf("promotions shown after one confirmed pick = %d, want 1", got)
	}

	// A chosen-result event for a plain (non-promo) result must not add to the count.
	h.HandleChosenInlineResult(context.Background(), sender, &models.Update{ChosenInlineResult: &models.ChosenInlineResult{
		ResultID: "0",
	}})
	if got := st.Snapshot(0).PromotionsShown; got != 1 {
		t.Errorf("promotions shown after a non-promo pick = %d, want still 1", got)
	}
}

func TestAnekHandler_HandleCallback_AcksPromotionButton(t *testing.T) {
	h, _ := newTestAnekHandler(t, `{"content":"joke"}`, 0.1)
	sender := &fakeSender{}

	h.HandleCallback(context.Background(), sender, &models.Update{CallbackQuery: &models.CallbackQuery{ID: "cb", Data: promotionCallbackData}})

	if len(sender.callbackAnswers) != 1 {
		t.Fatalf("expected callback ack, got %+v", sender.callbackAnswers)
	}
}
