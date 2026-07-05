package gcp

import "testing"

func TestResolveProject_GoogleCloudProject(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "my-project")
	t.Setenv("GCLOUD_PROJECT", "")
	if got := ResolveProject(); got != "my-project" {
		t.Errorf("ResolveProject() = %q, want %q", got, "my-project")
	}
}

func TestResolveProject_GcloudProject(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "fallback-project")
	if got := ResolveProject(); got != "fallback-project" {
		t.Errorf("ResolveProject() = %q, want %q", got, "fallback-project")
	}
}

func TestResolveProject_Priority(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "first")
	t.Setenv("GCLOUD_PROJECT", "second")
	if got := ResolveProject(); got != "first" {
		t.Errorf("ResolveProject() = %q, want %q (GOOGLE_CLOUD_PROJECT should take priority)", got, "first")
	}
}
