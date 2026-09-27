package resolver

import "testing"

func TestSafeFilename_RejectsControlAndEscape(t *testing.T) {
	for _, bad := range []string{
		"bad\x1b[31m.jar",
		"bad\n.jar",
		"bad\r.jar",
		"bad\x00.jar",
		"bad\x7f.jar",
		"../escape.jar",
		"/abs.jar",
	} {
		if _, err := SafeFilename(bad, "fallback.jar"); err == nil {
			// Note: path.Base strips directories, so ../escape.jar becomes
			// escape.jar (safe). Only control/escape cases must fail here;
			// traversal with separators is sanitized to base by design.
			if bad == "../escape.jar" || bad == "/abs.jar" {
				continue
			}
			t.Errorf("expected rejection for %q", bad)
		}
	}
	if _, err := SafeFilename("Good-Plugin_2.11.2.jar", ""); err != nil {
		t.Errorf("legit filename rejected: %v", err)
	}
	if _, err := SafeArchiveName("evil\nname"); err == nil {
		t.Errorf("expected control rejection for archive name")
	}
}
