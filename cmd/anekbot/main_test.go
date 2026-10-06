package main

import (
	"strings"
	"testing"
)

func TestVersionLine_IncludesBinaryNameAndVersionInfo(t *testing.T) {
	got := versionLine()
	if !strings.HasPrefix(got, "anekbot ") {
		t.Errorf("versionLine() = %q, want it to start with %q", got, "anekbot ")
	}
}
