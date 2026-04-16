package aiclient

import "testing"

func TestBuildSiteURL(t *testing.T) {
	got, err := buildSiteURL("http://ai-service:8205", "site_demo", "/status")
	if err != nil {
		t.Fatalf("buildSiteURL() error = %v", err)
	}
	want := "http://ai-service:8205/sites/site_demo/status"
	if got != want {
		t.Fatalf("buildSiteURL() = %q, want %q", got, want)
	}
}
