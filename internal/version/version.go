package version

import "runtime/debug"

// Version is set via -ldflags "-X github.com/ThreeHundredBugs/anekbot/internal/version.Version=..."
// for a local build it's "dev", since nothing else sets it.
var Version = "dev"

// Commit returns the git commit this binary was built from, with a "-dirty" suffix if the
// working tree had uncommitted changes at build time. It reads Go's build info, which the
// toolchain embeds automatically from VCS when building inside a git checkout (since Go
// 1.18) — no ldflags needed.
// Note that you should do "go run -buildvcs=true" to get a real
// commit instead of "unknown" out of a go-run'd binary.
func Commit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}

	var revision string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		return "unknown"
	}

	const shortLen = 12
	if len(revision) > shortLen {
		revision = revision[:shortLen]
	}
	if modified {
		revision += "-dirty"
	}
	return revision
}

// String formats Version and Commit for display, e.g. "dev (a1b2c3d4e5f6)".
func String() string {
	return Version + " (" + Commit() + ")"
}
