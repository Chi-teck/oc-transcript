package app

import "testing"

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
