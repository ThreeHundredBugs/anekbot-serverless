package version

import "testing"

func TestCommit_DoesNotPanicAndReturnsSomething(t *testing.T) {
	if got := Commit(); got == "" {
		t.Error("Commit() = \"\", want a non-empty value (at least \"unknown\")")
	}
}

func TestString_IncludesVersionAndCommit(t *testing.T) {
	got := String()
	if got == "" {
		t.Error("String() = \"\", want a non-empty value")
	}
}
