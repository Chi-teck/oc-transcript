package app

import (
	"runtime/debug"
	"testing"
)

func TestBuildVersionStamped(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })

	version = "1.2.3"
	if got := buildVersion(); got != "1.2.3" {
		t.Errorf("buildVersion() = %q, want the stamped release", got)
	}

	// Unstamped, the answer depends on how the test binary was built — a
	// commit, or "unknown" where there is no VCS to read. Either way it has
	// to say something.
	version = ""
	if got := buildVersion(); got == "" {
		t.Error("buildVersion() came back empty with nothing stamped")
	}
}

func TestVersionFromBuildInfo(t *testing.T) {
	const sha = "e4a3b0062f553433cf9061ddbd40d8142d4bcc57"

	cases := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		// `go install …@latest`: the proxy served a version, and there is no
		// checkout for a commit to have been read from.
		{
			"module version",
			&debug.BuildInfo{Main: debug.Module{Version: "v1.0.0"}},
			"v1.0.0",
		},
		// A checkout build records both, and Main.Version there is a
		// pseudo-version built around the very same commit.
		{
			"commit over pseudo-version",
			&debug.BuildInfo{
				Main:     debug.Module{Version: "v1.0.1-0.20260820160746-e4a3b0062f55"},
				Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: sha}},
			},
			"e4a3b0062f55",
		},
		{
			"dirty checkout",
			&debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: sha},
				{Key: "vcs.modified", Value: "true"},
			}},
			"e4a3b0062f55+dirty",
		},
		{
			"unpacked source tree",
			&debug.BuildInfo{Main: debug.Module{Version: "(devel)"}},
			"unknown",
		},
		{"nothing recorded", &debug.BuildInfo{}, "unknown"},
	}
	for _, c := range cases {
		if got := versionFromBuildInfo(c.info); got != c.want {
			t.Errorf("versionFromBuildInfo(%s) = %q, want %q", c.name, got, c.want)
		}
	}
}
