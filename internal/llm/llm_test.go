package llm

import (
	"context"
	"errors"
	"sync"
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

func TestParseAlgorithm(t *testing.T) {
	cases := map[string]Algorithm{"": Order, "order": Order, "round_robin": RoundRobin, "random": Random}
	for input, want := range cases {
		got, err := ParseAlgorithm(input)
		if err != nil || got != want {
			t.Errorf("ParseAlgorithm(%q) = %v, %v; want %v, nil", input, got, err, want)
		}
	}
	if _, err := ParseAlgorithm("bogus"); err == nil {
		t.Error("expected an error for an unknown algorithm")
	}
}

func TestAskOrderAlgorithmMatchesTodaysFixedOrder(t *testing.T) {
	a := &fakeProvider{name: "A", answer: "a"}
	b := &fakeProvider{name: "B", answer: "b"}
	l := New("prompt", Limits{}, a, b) // no SetAlgorithm call: Order is the zero value

	for i := 0; i < 3; i++ {
		if _, name, err := l.Ask(context.Background(), "q"); err != nil || name != "A" {
			t.Fatalf("call %d: got %q, %v; want A, nil", i, name, err)
		}
	}
}

func TestAskRoundRobinDistributesByWeight(t *testing.T) {
	a := &fakeProvider{name: "A", answer: "a"}
	b := &fakeProvider{name: "B", answer: "b"}
	l := New("prompt", Limits{}, WithWeight(a, 2), WithWeight(b, 1))
	l.SetAlgorithm(RoundRobin)

	counts := map[string]int{}
	const rounds = 30
	for i := 0; i < rounds; i++ {
		_, name, err := l.Ask(context.Background(), "q")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		counts[name]++
	}
	if counts["A"] != rounds*2/3 || counts["B"] != rounds/3 {
		t.Errorf("counts = %+v, want A:%d B:%d", counts, rounds*2/3, rounds/3)
	}
}

func TestAskRoundRobinFallsBackToNextInCycle(t *testing.T) {
	a := &fakeProvider{name: "A", err: errors.New("down")}
	b := &fakeProvider{name: "B", answer: "b"}
	l := New("prompt", Limits{}, a, b)
	l.SetAlgorithm(RoundRobin)

	_, name, err := l.Ask(context.Background(), "q")
	if err != nil || name != "B" {
		t.Fatalf("got %q, %v; want B, nil", name, err)
	}
	// The cursor must advance by exactly 1 for the whole request, not once per attempt: with
	// a 2-provider cycle, the next request's primary pick is B (cursor at 1), not wrapped
	// back to A (which is what an off-by-one "advance per attempt" bug would produce).
	if _, name, err := l.Ask(context.Background(), "q"); err != nil || name != "B" {
		t.Fatalf("second call: got %q, %v; want B, nil", name, err)
	}
	if a.calls != 1 {
		t.Errorf("A should only have been tried on the first request, got %d calls", a.calls)
	}
}

// stubProvider is a stateless Provider: unlike fakeProvider, it records nothing, so it's safe
// to share across goroutines without a race.
type stubProvider struct{ name, answer string }

func (p stubProvider) Name() string                                        { return p.name }
func (p stubProvider) Ask(context.Context, string, string) (string, error) { return p.answer, nil }

func TestRoundRobinCursorIsRaceSafe(t *testing.T) {
	l := New("prompt", Limits{MaxConcurrent: 50}, stubProvider{name: "A", answer: "a"}, stubProvider{name: "B", answer: "b"})
	l.SetAlgorithm(RoundRobin)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := l.Ask(context.Background(), "q"); err != nil {
				t.Errorf("Ask: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestAskRandomUsesWeights(t *testing.T) {
	a := &fakeProvider{name: "A", answer: "a"}
	b := &fakeProvider{name: "B", answer: "b"}
	l := New("prompt", Limits{}, WithWeight(a, 3), WithWeight(b, 1))
	l.SetAlgorithm(Random)

	// total weight 4; target 3.9 lands past A's weight(3), landing on B.
	l.randFloat = func() float64 { return 0.99 }
	if _, name, err := l.Ask(context.Background(), "q"); err != nil || name != "B" {
		t.Fatalf("got %q, %v; want B, nil", name, err)
	}
	// target 0 lands on A.
	l.randFloat = func() float64 { return 0 }
	if _, name, err := l.Ask(context.Background(), "q"); err != nil || name != "A" {
		t.Fatalf("got %q, %v; want A, nil", name, err)
	}
}

func TestAskRandomFallsBackAmongRemainingUntilExhausted(t *testing.T) {
	a := &fakeProvider{name: "A", err: errors.New("down")}
	b := &fakeProvider{name: "B", err: errors.New("down")}
	c := &fakeProvider{name: "C", answer: "c"}
	l := New("prompt", Limits{}, a, b, c)
	l.SetAlgorithm(Random)
	l.randFloat = func() float64 { return 0 } // always picks the first remaining candidate

	if _, name, err := l.Ask(context.Background(), "q"); err != nil || name != "C" {
		t.Fatalf("got %q, %v; want C, nil", name, err)
	}
	if a.calls != 1 || b.calls != 1 || c.calls != 1 {
		t.Errorf("calls = A:%d B:%d C:%d, want each exactly once", a.calls, b.calls, c.calls)
	}

	c.err = errors.New("down")
	if _, _, err := l.Ask(context.Background(), "q"); err == nil {
		t.Error("expected an error when every provider fails")
	}
}

func TestUnwrappedProviderDefaultsToWeightOne(t *testing.T) {
	a := &fakeProvider{name: "A", answer: "a"}
	b := &fakeProvider{name: "B", answer: "b"}
	l := New("prompt", Limits{}, a, b) // neither wrapped with WithWeight
	l.SetAlgorithm(RoundRobin)

	counts := map[string]int{}
	for i := 0; i < 20; i++ {
		_, name, _ := l.Ask(context.Background(), "q")
		counts[name]++
	}
	if counts["A"] != 10 || counts["B"] != 10 {
		t.Errorf("counts = %+v, want A:10 B:10", counts)
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
