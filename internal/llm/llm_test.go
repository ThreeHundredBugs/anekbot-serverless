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
	weight       int
	calls        int
	lastPrompt   string
	lastQuestion string
}

func (f *fakeProvider) Name() string {
	return f.name
}

func (f *fakeProvider) Weight() int {
	return f.weight
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

	if _, err := l.Ask(context.Background(), "hi"); err != nil {
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

	answer, err := l.Ask(context.Background(), "q")
	if err != nil || answer != "ok" {
		t.Errorf("got %q, %v; want ok, nil", answer, err)
	}

	third.err = errors.New("down")
	if _, err := l.Ask(context.Background(), "q"); err == nil {
		t.Error("expected an error when every provider fails")
	}
}

func TestAskTreatsEmptyAnswerAsFailureAndFallsBack(t *testing.T) {
	blank := &fakeProvider{name: "Blank", answer: "   "}
	fallback := &fakeProvider{name: "Fallback", answer: "ok"}
	l := New("prompt", Limits{}, blank, fallback)

	answer, err := l.Ask(context.Background(), "q")
	if err != nil || answer != "ok" {
		t.Errorf("got %q, %v; want ok, nil", answer, err)
	}

	fallback.answer = ""
	if _, err := l.Ask(context.Background(), "q"); err == nil {
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
		if answer, err := l.Ask(context.Background(), "q"); err != nil || answer != "a" {
			t.Fatalf("call %d: got %q, %v; want a, nil", i, answer, err)
		}
	}
}

func TestAskRoundRobinDistributesByWeight(t *testing.T) {
	a := &fakeProvider{name: "A", answer: "a", weight: 2}
	b := &fakeProvider{name: "B", answer: "b", weight: 1}
	l := New("prompt", Limits{}, a, b)
	l.SetAlgorithm(RoundRobin)

	counts := map[string]int{}
	const rounds = 30
	for i := 0; i < rounds; i++ {
		answer, err := l.Ask(context.Background(), "q")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		counts[answer]++
	}
	if counts["a"] != rounds*2/3 || counts["b"] != rounds/3 {
		t.Errorf("counts = %+v, want a:%d b:%d", counts, rounds*2/3, rounds/3)
	}
}

func TestAskRoundRobinFallsBackToNextInCycle(t *testing.T) {
	a := &fakeProvider{name: "A", err: errors.New("down"), weight: 1}
	b := &fakeProvider{name: "B", answer: "b", weight: 1}
	l := New("prompt", Limits{}, a, b)
	l.SetAlgorithm(RoundRobin)

	answer, err := l.Ask(context.Background(), "q")
	if err != nil || answer != "b" {
		t.Fatalf("got %q, %v; want b, nil", answer, err)
	}
	// The cursor must advance by exactly 1 for the whole request, not once per attempt: with
	// a 2-provider cycle, the next request's primary pick is B (cursor at 1), not wrapped
	// back to A (which is what an off-by-one "advance per attempt" bug would produce).
	if answer, err := l.Ask(context.Background(), "q"); err != nil || answer != "b" {
		t.Fatalf("second call: got %q, %v; want b, nil", answer, err)
	}
	if a.calls != 1 {
		t.Errorf("A should only have been tried on the first request, got %d calls", a.calls)
	}
}

// stubProvider is a stateless Provider: unlike fakeProvider, it records nothing, so it's safe
// to share across goroutines without a race.
type stubProvider struct{ name, answer string }

func (p stubProvider) Name() string                                        { return p.name }
func (p stubProvider) Weight() int                                         { return 1 }
func (p stubProvider) Ask(context.Context, string, string) (string, error) { return p.answer, nil }

func TestRoundRobinCursorIsRaceSafe(t *testing.T) {
	l := New("prompt", Limits{MaxConcurrent: 50}, stubProvider{name: "A", answer: "a"}, stubProvider{name: "B", answer: "b"})
	l.SetAlgorithm(RoundRobin)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := l.Ask(context.Background(), "q"); err != nil {
				t.Errorf("Ask: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestAskRandomUsesWeights(t *testing.T) {
	a := &fakeProvider{name: "A", answer: "a", weight: 3}
	b := &fakeProvider{name: "B", answer: "b", weight: 1}
	l := New("prompt", Limits{}, a, b)
	l.SetAlgorithm(Random)

	// total weight 4; target 3.9 lands past A's weight(3), landing on B.
	l.randFloat = func() float64 { return 0.99 }
	if answer, err := l.Ask(context.Background(), "q"); err != nil || answer != "b" {
		t.Fatalf("got %q, %v; want b, nil", answer, err)
	}
	// target 0 lands on A.
	l.randFloat = func() float64 { return 0 }
	if answer, err := l.Ask(context.Background(), "q"); err != nil || answer != "a" {
		t.Fatalf("got %q, %v; want a, nil", answer, err)
	}
}

func TestAskRandomFallsBackAmongRemainingUntilExhausted(t *testing.T) {
	a := &fakeProvider{name: "A", err: errors.New("down"), weight: 1}
	b := &fakeProvider{name: "B", err: errors.New("down"), weight: 1}
	c := &fakeProvider{name: "C", answer: "c", weight: 1}
	l := New("prompt", Limits{}, a, b, c)
	l.SetAlgorithm(Random)
	l.randFloat = func() float64 { return 0 } // always picks the first remaining candidate

	if answer, err := l.Ask(context.Background(), "q"); err != nil || answer != "c" {
		t.Fatalf("got %q, %v; want c, nil", answer, err)
	}
	if a.calls != 1 || b.calls != 1 || c.calls != 1 {
		t.Errorf("calls = A:%d B:%d C:%d, want each exactly once", a.calls, b.calls, c.calls)
	}

	c.err = errors.New("down")
	if _, err := l.Ask(context.Background(), "q"); err == nil {
		t.Error("expected an error when every provider fails")
	}
}

func TestAskRoundRobinSplitsEvenlyAtEqualWeight(t *testing.T) {
	l := New("prompt", Limits{}, stubProvider{name: "A", answer: "a"}, stubProvider{name: "B", answer: "b"})
	l.SetAlgorithm(RoundRobin)

	counts := map[string]int{}
	for i := 0; i < 20; i++ {
		answer, _ := l.Ask(context.Background(), "q")
		counts[answer]++
	}
	if counts["a"] != 10 || counts["b"] != 10 {
		t.Errorf("counts = %+v, want a:10 b:10", counts)
	}
}

func TestAskForEnforcesPerUserQuota(t *testing.T) {
	const perUserLimit = 3
	l := New("prompt", Limits{PerUserLimit: perUserLimit}, &fakeProvider{answer: "ok"})
	for i := 0; i < perUserLimit; i++ {
		if _, err := l.AskFor(context.Background(), 1, "q"); err != nil {
			t.Fatalf("request %d: unexpected error %v", i, err)
		}
	}
	if _, err := l.AskFor(context.Background(), 1, "q"); !errors.Is(err, ErrBusy) {
		t.Fatalf("over-quota request: got %v, want ErrBusy", err)
	}
	if _, err := l.AskFor(context.Background(), 2, "q"); err != nil {
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

	if _, err := l.AskFor(context.Background(), 1, "q"); !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v, want ErrBusy", err)
	}
}
