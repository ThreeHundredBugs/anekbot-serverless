package llm

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ThreeHundredBugs/anekbot/internal/flowcontrol"
	"github.com/ThreeHundredBugs/anekbot/internal/logging"
)

const (
	maxOutputTokens  = 2048
	maxResponseBytes = 1 << 20

	defaultMaxConcurrent = 16
	defaultPerUserLimit  = 5
	defaultPerUserWindow = time.Minute
	defaultPruneInterval = 30 * time.Minute
	defaultMaxUsers      = 100_000
)

var ErrBusy = errors.New("llm: rate limit or concurrency limit reached")
var ErrEmptyAnswer = errors.New("llm: provider returned an empty answer")

type UserID int64

type Provider interface {
	Name() string
	// Weight controls selection frequency under Algorithm RoundRobin/Random; ignored by Order.
	Weight() int
	Ask(ctx context.Context, systemPrompt, question string) (string, error)
}

// Recorder observes LLM activity for metrics. All methods must be safe for concurrent use.
type Recorder interface {
	// ObserveRequest records one provider attempt; primary is true for a request's first
	// attempt, false for a fallback attempt.
	ObserveRequest(provider string, ok, primary bool, duration time.Duration)
	ObserveFallback()
	// ObserveRateLimitRejection records a rejection; scope is "per_user" or "concurrency".
	ObserveRateLimitRejection(scope string)
	IncConcurrency()
	DecConcurrency()
}

type noopRecorder struct{}

func (noopRecorder) ObserveRequest(string, bool, bool, time.Duration) {}
func (noopRecorder) ObserveFallback()                                 {}
func (noopRecorder) ObserveRateLimitRejection(string)                 {}
func (noopRecorder) IncConcurrency()                                  {}
func (noopRecorder) DecConcurrency()                                  {}

// zero value in any field falls back to its default.
type Limits struct {
	MaxConcurrent int
	PerUserLimit  int
	PerUserWindow time.Duration
	MaxUsers      int
	PruneInterval time.Duration
}

func (l Limits) withDefaults() Limits {
	if l.MaxConcurrent <= 0 {
		l.MaxConcurrent = defaultMaxConcurrent
	}
	if l.PerUserLimit <= 0 {
		l.PerUserLimit = defaultPerUserLimit
	}
	if l.PerUserWindow <= 0 {
		l.PerUserWindow = defaultPerUserWindow
	}
	if l.MaxUsers <= 0 {
		l.MaxUsers = defaultMaxUsers
	}
	if l.PruneInterval <= 0 {
		l.PruneInterval = defaultPruneInterval
	}
	return l
}

type LLM struct {
	systemPrompt string
	providers    []Provider
	recorder     Recorder

	algorithm Algorithm
	cycle     []int // weighted round-robin cycle; built by SetAlgorithm(RoundRobin)
	cursor    atomic.Uint64
	randFloat func() float64

	concurrency *flowcontrol.Semaphore
	perUser     *flowcontrol.PerKeyLimiter[UserID]
}

func New(systemPrompt string, limits Limits, providers ...Provider) *LLM {
	limits = limits.withDefaults()
	return &LLM{
		systemPrompt: systemPrompt,
		providers:    providers,
		recorder:     noopRecorder{},
		randFloat:    rand.Float64,
		concurrency:  flowcontrol.NewSemaphore(limits.MaxConcurrent),
		perUser: &flowcontrol.PerKeyLimiter[UserID]{
			Limit:         limits.PerUserLimit,
			Window:        limits.PerUserWindow,
			PruneInterval: limits.PruneInterval,
			MaxKeys:       limits.MaxUsers,
		},
	}
}

func (l *LLM) SetRecorder(r Recorder) {
	if r == nil {
		r = noopRecorder{}
	}
	l.recorder = r
}

// SetAlgorithm picks how Ask orders provider attempts; the zero value (Order) is today's
// fixed config-order fallback. Like SetRecorder, this is one-time startup wiring and must
// not be called concurrently with Ask.
func (l *LLM) SetAlgorithm(algo Algorithm) {
	l.algorithm = algo
	if algo == RoundRobin {
		l.cycle = buildCycle(l.providers)
	}
}

func buildCycle(providers []Provider) []int {
	cycle := make([]int, 0, len(providers))
	for i, p := range providers {
		for j := 0; j < p.Weight(); j++ {
			cycle = append(cycle, i)
		}
	}
	return cycle
}

func (l *LLM) AskFor(ctx context.Context, userID UserID, question string) (answer string, err error) {
	if !l.perUser.Allow(userID) {
		logging.Debugf("llm: user %d over quota, rejecting", userID)
		l.recorder.ObserveRateLimitRejection("per_user")
		return "", ErrBusy
	}
	release, ok := l.concurrency.TryAcquire()
	if !ok {
		logging.Warnf("llm: at max concurrency, rejecting request for user %d", userID)
		l.recorder.ObserveRateLimitRejection("concurrency")
		return "", ErrBusy
	}
	defer release()

	l.recorder.IncConcurrency()
	defer l.recorder.DecConcurrency()

	return l.Ask(ctx, question)
}

func (l *LLM) Ask(ctx context.Context, question string) (answer string, err error) {
	for attempt, idx := range l.attemptOrder() {
		provider := l.providers[idx]
		start := time.Now()
		answer, err = provider.Ask(ctx, l.systemPrompt, question)
		if err == nil && strings.TrimSpace(answer) == "" {
			err = ErrEmptyAnswer
		}
		l.recorder.ObserveRequest(provider.Name(), err == nil, attempt == 0, time.Since(start))

		if err == nil {
			logging.Debugf("llm: %s answered", provider.Name())
			if attempt > 0 {
				l.recorder.ObserveFallback()
			}
			return answer, nil
		}
		if attempt == 0 {
			logging.Warnf("llm: primary provider: %v", err)
		} else {
			logging.Warnf("llm: fallback provider %s: %v", provider.Name(), err)
		}
	}
	return "", err
}

func (l *LLM) attemptOrder() []int {
	switch l.algorithm {
	case RoundRobin:
		return l.roundRobinOrder()
	case Random:
		return l.randomOrder()
	default:
		return identityOrder(len(l.providers))
	}
}

func identityOrder(n int) []int {
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	return order
}

// roundRobinOrder advances the shared cursor exactly once per request (not per attempt), so a
// request that falls back doesn't throw off the next request's starting point, then walks the
// weighted cycle from there, deduping so each provider is tried at most once.
func (l *LLM) roundRobinOrder() []int {
	n := len(l.cycle)
	if n == 0 {
		return nil
	}
	start := l.cursor.Add(1) - 1
	seen := make(map[int]bool, len(l.providers))
	order := make([]int, 0, len(l.providers))
	for i := 0; i < n && len(order) < len(l.providers); i++ {
		idx := l.cycle[(int(start)+i)%n]
		if !seen[idx] {
			seen[idx] = true
			order = append(order, idx)
		}
	}
	return order
}

// randomOrder draws providers without replacement, each round weighted among those not yet
// picked, the same "subtract weight until negative" idiom Promotions.Pick uses.
func (l *LLM) randomOrder() []int {
	remaining := identityOrder(len(l.providers))
	weights := make([]int, len(remaining))
	for i, idx := range remaining {
		weights[i] = l.providers[idx].Weight()
	}

	order := make([]int, 0, len(remaining))
	for len(remaining) > 0 {
		total := 0
		for _, w := range weights {
			total += w
		}
		target := l.randFloat() * float64(total)
		pick := 0
		for ; pick < len(remaining)-1; pick++ {
			target -= float64(weights[pick])
			if target < 0 {
				break
			}
		}
		order = append(order, remaining[pick])
		remaining = append(remaining[:pick], remaining[pick+1:]...)
		weights = append(weights[:pick], weights[pick+1:]...)
	}
	return order
}
