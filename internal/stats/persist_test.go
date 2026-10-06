package stats

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadFile_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")

	s := New()
	s.RecordAnek(1, "alice", "message", "classic")
	s.RecordAnek(1, "alice", "inline", "ai")
	s.RecordAnek(2, "bob", "message", "classic")
	s.RecordQuestionAnswered(1, "alice")
	s.RecordSwearingReaction()
	s.RecordPromotionShown()
	s.ObserveRequest("gemini", true, true, time.Millisecond)
	s.ObserveRequest("gemini", false, true, time.Millisecond)
	s.ObserveFallback()
	s.ObserveRateLimitRejection("concurrency")
	s.ObserveRateLimitRejection("per_user")

	if err := s.SaveFile(path); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	loaded := New()
	if err := loaded.LoadFile(path); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	want := s.Snapshot(10)
	got := loaded.Snapshot(10)
	// Gauges aren't persisted; zero them on both sides before comparing the rest.
	want.LLMConcurrencyInUse, got.LLMConcurrencyInUse = 0, 0

	if got.TotalAneks != want.TotalAneks || got.TotalAIAneks != want.TotalAIAneks {
		t.Errorf("aneks = %+v, want %+v", got, want)
	}
	if got.TotalUsers != want.TotalUsers {
		t.Errorf("TotalUsers = %d, want %d", got.TotalUsers, want.TotalUsers)
	}
	if got.QuestionsAnswered != want.QuestionsAnswered || got.SwearingReactions != want.SwearingReactions ||
		got.PromotionsShown != want.PromotionsShown {
		t.Errorf("activity counters = %+v, want %+v", got, want)
	}
	if got.LLMRequestsOK != want.LLMRequestsOK || got.LLMRequestsError != want.LLMRequestsError ||
		got.LLMFallbacks != want.LLMFallbacks {
		t.Errorf("llm counters = %+v, want %+v", got, want)
	}
	if got.RateLimitRejectionsPerUser != want.RateLimitRejectionsPerUser ||
		got.RateLimitRejectionsConcurrency != want.RateLimitRejectionsConcurrency {
		t.Errorf("rate limit counters = %+v, want %+v", got, want)
	}
	if len(got.TopUsers) != len(want.TopUsers) {
		t.Fatalf("TopUsers = %+v, want %+v", got.TopUsers, want.TopUsers)
	}
	for i := range want.TopUsers {
		if got.TopUsers[i] != want.TopUsers[i] {
			t.Errorf("TopUsers[%d] = %+v, want %+v", i, got.TopUsers[i], want.TopUsers[i])
		}
	}
}

func TestLoadFile_MissingFileIsNotAnError(t *testing.T) {
	s := New()
	err := s.LoadFile(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("LoadFile: %v, want nil for a missing file", err)
	}
	if snap := s.Snapshot(10); snap.TotalAneks != 0 {
		t.Errorf("TotalAneks = %d, want 0 on a fresh Stats", snap.TotalAneks)
	}
}

func TestLoadFile_InvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := New().LoadFile(path); err == nil {
		t.Error("expected an error for invalid JSON")
	}
}

func TestSaveFile_AtomicNoLeftoverTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stats.json")

	if err := New().SaveFile(path); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "stats.json" {
		t.Errorf("dir entries = %v, want exactly stats.json (no leftover temp file)", entries)
	}
}

func TestSaveFile_OverwritesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")

	s1 := New()
	s1.RecordAnek(1, "alice", "message", "classic")
	if err := s1.SaveFile(path); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	s2 := New()
	for i := 0; i < 5; i++ {
		s2.RecordAnek(2, "bob", "message", "classic")
	}
	if err := s2.SaveFile(path); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	loaded := New()
	if err := loaded.LoadFile(path); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got := loaded.Snapshot(10).TotalAneks; got != 5 {
		t.Errorf("TotalAneks = %d, want 5 (from the second save, not the first)", got)
	}
}

func TestLoadFile_CapsPerUserAtMaxTrackedUsers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")

	s := New()
	for i := 0; i < maxTrackedUsers+20; i++ {
		s.RecordAnek(UserID(i+1), "user", "message", "classic")
	}
	if err := s.SaveFile(path); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	loaded := New()
	if err := loaded.LoadFile(path); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got := loaded.Snapshot(0).TotalUsers; got != maxTrackedUsers {
		t.Errorf("TotalUsers = %d, want it capped at %d", got, maxTrackedUsers)
	}
}
