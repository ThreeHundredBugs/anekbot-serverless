package llm

import (
	"context"
	"errors"
	"testing"
)

// fakeProvider is a canned Provider used to test LLM's fallback and limiting behavior without
// real HTTP calls.
type fakeProvider struct {
	name         string
	answer       string
	err          error
	calls        int
	lastPrompt   string
	lastQuestion string
}

func (f *fakeProvider) Name() string {
	return f.name
}

func (f *fakeProvider) Ask(_ context.Context, systemPrompt, question string) (string, error) {
	f.calls++
	f.lastPrompt = systemPrompt
	f.lastQuestion = question
	if f.err != nil {
		return "", f.err
	}
	return f.answer, nil
}

func TestAskPassesSystemPromptToProvider(t *testing.T) {
	provider := &fakeProvider{name: "Fake", answer: "ok"}
	l := New("be nice", Limits{}, provider)

	if _, _, err := l.Ask(context.Background(), "hi"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if provider.lastPrompt != "be nice" {
		t.Errorf("system prompt = %q, want %q", provider.lastPrompt, "be nice")
	}
	if provider.lastQuestion != "hi" {
		t.Errorf("question = %q, want %q", provider.lastQuestion, "hi")
	}
}

func TestAskUsesFirstWorkingProvider(t *testing.T) {
	first := &fakeProvider{name: "First", err: errors.New("down")}
	second := &fakeProvider{name: "Second", err: errors.New("down")}
	third := &fakeProvider{name: "Third", answer: "ok"}
	l := New("prompt", Limits{}, first, second, third)

	answer, name, err := l.Ask(context.Background(), "q")
	if err != nil || answer != "ok" || name != "Third" {
		t.Errorf("got %q, %q, %v; want ok, Third, nil", answer, name, err)
	}

	third.err = errors.New("down")
	if _, _, err := l.Ask(context.Background(), "q"); err == nil {
		t.Error("expected an error when every provider fails")
	}
}

func TestAskTreatsEmptyAnswerAsFailureAndFallsBack(t *testing.T) {
	blank := &fakeProvider{name: "Blank", answer: "   "}
	fallback := &fakeProvider{name: "Fallback", answer: "ok"}
	l := New("prompt", Limits{}, blank, fallback)

	answer, name, err := l.Ask(context.Background(), "q")
	if err != nil || answer != "ok" || name != "Fallback" {
		t.Errorf("got %q, %q, %v; want ok, Fallback, nil", answer, name, err)
	}

	fallback.answer = ""
	if _, _, err := l.Ask(context.Background(), "q"); err == nil {
		t.Error("expected an error when every provider returns a blank answer")
	}
}

func TestAskForEnforcesPerUserQuota(t *testing.T) {
	const perUserLimit = 3
	l := New("prompt", Limits{PerUserLimit: perUserLimit}, &fakeProvider{answer: "ok"})
	for i := 0; i < perUserLimit; i++ {
		if _, _, err := l.AskFor(context.Background(), 1, "q"); err != nil {
			t.Fatalf("request %d: unexpected error %v", i, err)
		}
	}
	if _, _, err := l.AskFor(context.Background(), 1, "q"); !errors.Is(err, ErrBusy) {
		t.Fatalf("over-quota request: got %v, want ErrBusy", err)
	}
	if _, _, err := l.AskFor(context.Background(), 2, "q"); err != nil {
		t.Fatalf("other user must not be limited: %v", err)
	}
}

func TestAskForRejectsWhenAllSlotsBusy(t *testing.T) {
	const maxConcurrent = 2
	l := New("prompt", Limits{MaxConcurrent: maxConcurrent}, &fakeProvider{answer: "ok"})
	var releases []func()
	for i := 0; i < maxConcurrent; i++ {
		release, ok := l.concurrency.TryAcquire()
		if !ok {
			t.Fatalf("failed to acquire slot %d", i)
		}
		releases = append(releases, release)
	}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()

	if _, _, err := l.AskFor(context.Background(), 1, "q"); !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v, want ErrBusy", err)
	}
}
