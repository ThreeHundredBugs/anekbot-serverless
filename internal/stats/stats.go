package stats

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VictoriaMetrics/metrics"
)

type UserID int64

type Stats struct {
	set *metrics.Set

	llmFallbackTotal    *metrics.Counter
	swearingReactions   *metrics.Counter
	promotionsShown     *metrics.Counter
	questionsAnswered   *metrics.Counter
	llmConcurrencyInUse *metrics.Gauge
	// trackedUsers mirrors len(perUser); capped at maxTrackedUsers, so it's not a true
	// distinct-user count once that cap is reached.
	trackedUsers *metrics.Gauge

	totalAneksClassic    atomic.Int64
	totalAneksAI         atomic.Int64
	llmRequestsOK        atomic.Int64
	llmRequestsError     atomic.Int64
	rateLimitPerUser     atomic.Int64
	rateLimitConcurrency atomic.Int64

	mu      sync.Mutex
	perUser map[UserID]*userCount

	// llmProviders is populated once via RegisterLLMProviders, before any concurrent use;
	// ObserveRequest only ever reads the map and bumps a counter already in it, so no mutex
	// guards the map itself. The counters are atomics so those bumps are still race-free.
	llmProviders map[string]*llmProviderCounters

	// startedAt is set once in New and never persisted: uptime resets on every restart.
	startedAt time.Time
}

type llmProviderCounters struct {
	successPrimary  atomic.Int64
	successFallback atomic.Int64
	failPrimary     atomic.Int64
	failFallback    atomic.Int64
}

type userCount struct {
	username string
	count    int64
}

func New() *Stats {
	metrics.ExposeMetadata(true)

	set := metrics.NewSet()
	s := &Stats{
		set:          set,
		perUser:      make(map[UserID]*userCount),
		llmProviders: make(map[string]*llmProviderCounters),
		startedAt:    time.Now(),

		llmFallbackTotal:  set.NewCounter("anekbot_llm_fallback_total"),
		swearingReactions: set.NewCounter("anekbot_swearing_reactions_total"),
		promotionsShown:   set.NewCounter("anekbot_promotions_shown_total"),
		questionsAnswered: set.NewCounter("anekbot_questions_answered_total"),
	}
	s.llmConcurrencyInUse = set.NewGauge("anekbot_llm_concurrency_in_use", nil)
	s.trackedUsers = set.NewGauge("anekbot_tracked_users", nil)
	return s
}

// RecordAnek attributes a delivered joke to user, and to the totals for source
// ("message"/"inline") and kind ("classic"/"ai"). s may be nil.
func (s *Stats) RecordAnek(user UserID, username, source, kind string) {
	if s == nil {
		return
	}
	s.set.GetOrCreateCounter(labeled("anekbot_aneks_sent_total", "source", source, "kind", kind)).Inc()
	if kind == "ai" {
		s.totalAneksAI.Add(1)
	} else {
		s.totalAneksClassic.Add(1)
	}
	s.recordUser(user, username)
}

func (s *Stats) RecordQuestionAnswered(user UserID, username string) {
	if s == nil {
		return
	}
	s.questionsAnswered.Inc()
	s.recordUser(user, username)
}

func (s *Stats) RecordSwearingReaction() {
	if s == nil {
		return
	}
	s.swearingReactions.Inc()
}

func (s *Stats) RecordPromotionShown() {
	if s == nil {
		return
	}
	s.promotionsShown.Inc()
}

// LLMProviderStats breaks down a single provider's requests by outcome and by whether the
// request was the first (primary) attempt or a fallback from an earlier provider's failure.
type LLMProviderStats struct {
	Provider        string
	SuccessPrimary  int64
	SuccessFallback int64
	FailPrimary     int64
	FailFallback    int64
}

func (p LLMProviderStats) Total() int64 {
	return p.SuccessPrimary + p.SuccessFallback + p.FailPrimary + p.FailFallback
}

// RegisterLLMProviders pre-allocates a counter for each name, so ObserveRequest never has to
// mutate llmProviders itself at request time. Call once at startup with every configured
// provider's name, before the bot starts handling updates and before LoadFile (LoadFile only
// restores a provider's counts if it's already registered); like SetRecorder/SetAlgorithm
// elsewhere in this codebase, it is not safe to call concurrently with ObserveRequest.
func (s *Stats) RegisterLLMProviders(names []string) {
	if s == nil {
		return
	}
	for _, name := range names {
		if _, ok := s.llmProviders[name]; !ok {
			s.llmProviders[name] = &llmProviderCounters{}
		}
	}
}

// ObserveRequest records one provider attempt. provider must have been passed to
// RegisterLLMProviders; an unregistered name is silently dropped from the per-provider
// breakdown (it still counts toward the global llmRequestsOK/Error totals below).
func (s *Stats) ObserveRequest(provider string, ok, isPrimary bool, duration time.Duration) {
	if s == nil {
		return
	}
	status := "error"
	if ok {
		status = "ok"
	}
	attempt := "fallback"
	if isPrimary {
		attempt = "primary"
	}
	s.set.GetOrCreateCounter(labeled("anekbot_llm_requests_total", "provider", provider, "status", status, "attempt", attempt)).Inc()
	s.set.GetOrCreateHistogram(labeled("anekbot_llm_request_duration_seconds", "provider", provider)).Update(duration.Seconds())
	if ok {
		s.llmRequestsOK.Add(1)
	} else {
		s.llmRequestsError.Add(1)
	}

	providerCounters := s.llmProviders[provider]
	if providerCounters == nil {
		return
	}
	switch {
	case ok && isPrimary:
		providerCounters.successPrimary.Add(1)
	case ok && !isPrimary:
		providerCounters.successFallback.Add(1)
	case !ok && isPrimary:
		providerCounters.failPrimary.Add(1)
	default:
		providerCounters.failFallback.Add(1)
	}
}

func (s *Stats) ObserveFallback() {
	if s == nil {
		return
	}
	s.llmFallbackTotal.Inc()
}

func (s *Stats) ObserveRateLimitRejection(scope string) {
	if s == nil {
		return
	}
	s.set.GetOrCreateCounter(labeled("anekbot_rate_limit_rejections_total", "scope", scope)).Inc()
	if scope == "concurrency" {
		s.rateLimitConcurrency.Add(1)
	} else {
		s.rateLimitPerUser.Add(1)
	}
}

func (s *Stats) IncConcurrency() {
	if s != nil {
		s.llmConcurrencyInUse.Inc()
	}
}

func (s *Stats) DecConcurrency() {
	if s != nil {
		s.llmConcurrencyInUse.Dec()
	}
}

// maxTrackedUsers bounds perUser's memory: without a cap, a distinct entry accumulates for
// every user who ever triggers a joke and is never evicted, growing forever.
const maxTrackedUsers = 100

func (s *Stats) recordUser(user UserID, username string) {
	if user == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if uc, ok := s.perUser[user]; ok {
		if username != "" {
			uc.username = username
		}
		uc.count++
		return
	}

	if len(s.perUser) >= maxTrackedUsers {
		// Space-Saving algorithm: evict the least-active tracked user and seed the
		// newcomer at the evicted count + 1, so a genuinely active new user can still
		// climb into the top N instead of being permanently locked out once the table
		// fills up. This makes counts for users that enter this way approximate (an
		// overestimate, never an undercount), which is fine for an admin-facing leaderboard.
		minUser, minCount := s.minTrackedLocked()
		delete(s.perUser, minUser)
		s.perUser[user] = &userCount{username: username, count: minCount + 1}
		return
	}

	s.perUser[user] = &userCount{username: username, count: 1}
	s.trackedUsers.Set(float64(len(s.perUser)))
}

// minTrackedLocked returns the tracked user with the lowest count. s.mu must be held, and
// s.perUser must be non-empty.
func (s *Stats) minTrackedLocked() (user UserID, count int64) {
	first := true
	for id, uc := range s.perUser {
		if first || uc.count < count {
			user, count, first = id, uc.count, false
		}
	}
	return user, count
}

type UserTotal struct {
	UserID   UserID
	Username string
	Count    int64
}

type Snapshot struct {
	// Uptime is how long this process has been running; it resets on every restart.
	Uptime time.Duration

	TotalAneks   int64
	TotalAIAneks int64
	// TotalUsers is capped at maxTrackedUsers; see recordUser.
	TotalUsers int
	TopUsers   []UserTotal

	QuestionsAnswered int64
	SwearingReactions int64
	PromotionsShown   int64

	LLMRequestsOK                  int64
	LLMRequestsError               int64
	LLMFallbacks                   int64
	LLMConcurrencyInUse            int64
	RateLimitRejectionsPerUser     int64
	RateLimitRejectionsConcurrency int64
	// LLMProviders is sorted by Provider name.
	LLMProviders []LLMProviderStats
}

// Snapshot returns current totals and the topN users by recorded anek count. s may be nil.
func (s *Stats) Snapshot(topN int) Snapshot {
	if s == nil {
		return Snapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	totals := make([]UserTotal, 0, len(s.perUser))
	for id, uc := range s.perUser {
		totals = append(totals, UserTotal{UserID: id, Username: uc.username, Count: uc.count})
	}
	sort.Slice(totals, func(i, j int) bool {
		if totals[i].Count != totals[j].Count {
			return totals[i].Count > totals[j].Count
		}
		return totals[i].UserID < totals[j].UserID
	})
	if len(totals) > topN {
		totals = totals[:topN]
	}

	providers := make([]LLMProviderStats, 0, len(s.llmProviders))
	for name, c := range s.llmProviders {
		providers = append(providers, LLMProviderStats{
			Provider:        name,
			SuccessPrimary:  c.successPrimary.Load(),
			SuccessFallback: c.successFallback.Load(),
			FailPrimary:     c.failPrimary.Load(),
			FailFallback:    c.failFallback.Load(),
		})
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Provider < providers[j].Provider })

	return Snapshot{
		Uptime: time.Since(s.startedAt),

		TotalAneks:   s.totalAneksClassic.Load() + s.totalAneksAI.Load(),
		TotalAIAneks: s.totalAneksAI.Load(),
		TotalUsers:   len(s.perUser),
		TopUsers:     totals,

		QuestionsAnswered: int64(s.questionsAnswered.Get()),
		SwearingReactions: int64(s.swearingReactions.Get()),
		PromotionsShown:   int64(s.promotionsShown.Get()),

		LLMRequestsOK:                  s.llmRequestsOK.Load(),
		LLMRequestsError:               s.llmRequestsError.Load(),
		LLMFallbacks:                   int64(s.llmFallbackTotal.Get()),
		LLMConcurrencyInUse:            int64(s.llmConcurrencyInUse.Get()),
		RateLimitRejectionsPerUser:     s.rateLimitPerUser.Load(),
		RateLimitRejectionsConcurrency: s.rateLimitConcurrency.Load(),
		LLMProviders:                   providers,
	}
}

// Handler serves Prometheus metrics, requiring "Authorization: Bearer <token>" to match
// token via constant-time comparison. token must be non-empty; callers should not mount
// this handler otherwise.
func (s *Stats) Handler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validBearerToken(r, token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.set.WritePrometheus(w)
		metrics.WriteGoMetrics(w)
		metrics.WriteProcessMetrics(w)
	})
}

func validBearerToken(r *http.Request, token string) bool {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	got := auth[len(prefix):]
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// labeled builds a VictoriaMetrics-style metric name with inline labels, e.g.
// labeled("foo", "a", "1", "b", "2") -> `foo{a="1",b="2"}`. kv must have an even length.
func labeled(name string, kv ...string) string {
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('{')
	for i := 0; i < len(kv); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=%q", kv[i], kv[i+1])
	}
	b.WriteByte('}')
	return b.String()
}
