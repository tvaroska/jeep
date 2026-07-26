package gcp

import "testing"

func TestResolveProject_Unresolved(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	// Break the gcloud lookup so the exec fallback errors and we hit the final
	// empty return.
	t.Setenv("PATH", "")
	if got := ResolveProject(); got != "" {
		t.Errorf("ResolveProject() = %q, want empty when nothing is set", got)
	}
}
