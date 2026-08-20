package app

import (
	"errors"
	"runtime/debug"
)

// version is the release this binary was cut from, stamped by the linker at
// release time (-X github.com/Chi-teck/oc-transcript/internal/app.version=…,
// see .goreleaser.yml). Every other build leaves it empty, and buildVersion
// falls back to what the toolchain recorded.
var version string

// errVersionPrinted, like pflag.ErrHelp, is not a failure: the command line
// asked for something that has already gone to stdout.
var errVersionPrinted = errors.New("version printed")

// buildVersion names the build as well as it can be named. A release says its
// tag; a `go build` inside a checkout says the commit it came from, marked
// +dirty when the tree had uncommitted changes; a `go install …@version` says
// the module version it was served as; a build from an unpacked source tree,
// with neither to read, can only say "unknown".
func buildVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return versionFromBuildInfo(info)
}

// versionFromBuildInfo reads the build back out of what the toolchain recorded.
// The commit comes first because it is the narrower answer: a checkout build
// records both, and there Main.Version is a pseudo-version built around that
// same commit. Only an install by module path — `go install …@latest` — has a
// version and no VCS to have taken it from.
func versionFromBuildInfo(info *debug.BuildInfo) string {
	var revision, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "+dirty"
			}
		}
	}
	if revision == "" {
		// "(devel)" is what a build with nothing to stamp reports; it names
		// the build no better than "unknown" does.
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
		return "unknown"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	return revision + dirty
}
