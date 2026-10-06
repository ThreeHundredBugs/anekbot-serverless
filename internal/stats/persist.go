package stats

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// persistedState is the subset of Stats saved to disk across restarts: the running totals,
// per-user leaderboard, and per-LLM-provider breakdown behind Snapshot. Point-in-time gauges
// (LLM concurrency, tracked users) and the source/kind Prometheus label breakdown aren't
// persisted — they either reset naturally on restart or aren't meaningful to restore.
type persistedState struct {
	TotalAneksClassic    int64                                   `json:"total_aneks_classic"`
	TotalAneksAI         int64                                   `json:"total_aneks_ai"`
	LLMRequestsOK        int64                                   `json:"llm_requests_ok"`
	LLMRequestsError     int64                                   `json:"llm_requests_error"`
	RateLimitPerUser     int64                                   `json:"rate_limit_per_user"`
	RateLimitConcurrency int64                                   `json:"rate_limit_concurrency"`
	LLMFallbackTotal     uint64                                  `json:"llm_fallback_total"`
	SwearingReactions    uint64                                  `json:"swearing_reactions"`
	PromotionsShown      uint64                                  `json:"promotions_shown"`
	QuestionsAnswered    uint64                                  `json:"questions_answered"`
	PerUser              map[UserID]persistedUser                `json:"per_user"`
	LLMProviders         map[string]persistedLLMProviderCounters `json:"llm_providers"`
}

type persistedUser struct {
	Username string `json:"username"`
	Count    int64  `json:"count"`
}

type persistedLLMProviderCounters struct {
	SuccessPrimary  int64 `json:"success_primary"`
	SuccessFallback int64 `json:"success_fallback"`
	FailPrimary     int64 `json:"fail_primary"`
	FailFallback    int64 `json:"fail_fallback"`
}

// SaveFile atomically writes s's persistable state to path: it writes to a temp file in the
// same directory and renames it into place, so a crash mid-write, or a concurrent read of
// path, never observes a corrupt or partial file.
func (s *Stats) SaveFile(path string) error {
	data, err := json.MarshalIndent(s.exportState(), "", "  ")
	if err != nil {
		return fmt.Errorf("marshal stats: %w", err)
	}
	return atomicWriteFile(path, data)
}

// LoadFile restores state saved by SaveFile into s. A missing file is not an error: it just
// means there's nothing to restore yet, e.g. on the very first run.
func (s *Stats) LoadFile(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read stats file: %w", err)
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("parse stats file %s: %w", path, err)
	}
	s.importState(state)
	return nil
}

func (s *Stats) exportState() persistedState {
	s.mu.Lock()
	perUser := make(map[UserID]persistedUser, len(s.perUser))
	for id, uc := range s.perUser {
		perUser[id] = persistedUser{Username: uc.username, Count: uc.count}
	}
	s.mu.Unlock()

	// llmProviders' key set is fixed at startup by RegisterLLMProviders, so reading it here
	// needs no lock, same as ObserveRequest/Snapshot.
	providers := make(map[string]persistedLLMProviderCounters, len(s.llmProviders))
	for name, c := range s.llmProviders {
		providers[name] = persistedLLMProviderCounters{
			SuccessPrimary:  c.successPrimary.Load(),
			SuccessFallback: c.successFallback.Load(),
			FailPrimary:     c.failPrimary.Load(),
			FailFallback:    c.failFallback.Load(),
		}
	}

	return persistedState{
		TotalAneksClassic:    s.totalAneksClassic.Load(),
		TotalAneksAI:         s.totalAneksAI.Load(),
		LLMRequestsOK:        s.llmRequestsOK.Load(),
		LLMRequestsError:     s.llmRequestsError.Load(),
		RateLimitPerUser:     s.rateLimitPerUser.Load(),
		RateLimitConcurrency: s.rateLimitConcurrency.Load(),
		LLMFallbackTotal:     s.llmFallbackTotal.Get(),
		SwearingReactions:    s.swearingReactions.Get(),
		PromotionsShown:      s.promotionsShown.Get(),
		QuestionsAnswered:    s.questionsAnswered.Get(),
		PerUser:              perUser,
		LLMProviders:         providers,
	}
}

func (s *Stats) importState(state persistedState) {
	s.totalAneksClassic.Store(state.TotalAneksClassic)
	s.totalAneksAI.Store(state.TotalAneksAI)
	s.llmRequestsOK.Store(state.LLMRequestsOK)
	s.llmRequestsError.Store(state.LLMRequestsError)
	s.rateLimitPerUser.Store(state.RateLimitPerUser)
	s.rateLimitConcurrency.Store(state.RateLimitConcurrency)
	s.llmFallbackTotal.Set(state.LLMFallbackTotal)
	s.swearingReactions.Set(state.SwearingReactions)
	s.promotionsShown.Set(state.PromotionsShown)
	s.questionsAnswered.Set(state.QuestionsAnswered)

	s.mu.Lock()
	for id, pu := range state.PerUser {
		if len(s.perUser) >= maxTrackedUsers {
			break
		}
		s.perUser[id] = &userCount{username: pu.Username, count: pu.Count}
	}
	s.trackedUsers.Set(float64(len(s.perUser)))
	s.mu.Unlock()

	// A provider name no longer in s.llmProviders (renamed/removed from config since the
	// file was saved) is silently dropped, same as ObserveRequest does for an unknown name.
	for name, pc := range state.LLMProviders {
		if c := s.llmProviders[name]; c != nil {
			c.successPrimary.Store(pc.SuccessPrimary)
			c.successFallback.Store(pc.SuccessFallback)
			c.failPrimary.Store(pc.FailPrimary)
			c.failFallback.Store(pc.FailFallback)
		}
	}
}

// atomicWriteFile writes data to path by writing it to a temp file in the same directory and
// renaming it over path; rename(2) is atomic within a directory, so readers of path always see
// either the old content or the new one in full, never a partial write.
func atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file to %s: %w", path, err)
	}
	return nil
}
