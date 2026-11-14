package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.json":
			w.Write([]byte(`{"version": "1.2.0", "changelog": "fixes", "url": "https://example.com/m.zip"}`))
		case "/bad.json":
			w.Write([]byte(`{"version": "soon", "url": ""}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	tests := []struct {
		url, version string
		newer, err   bool
	}{
		{"/ok.json", "1.1.9", true, false},
		{"/ok.json", "1.2.0", false, false},
		{"/ok.json", "2.0.0", false, false},
		{"/bad.json", "1.0.0", false, true},
		{"/missing.json", "1.0.0", false, true},
	}
	for _, tt := range tests {
		m := &Mod{Dir: "m", Manifest: &Manifest{Version: tt.version, UpdateCheck: srv.URL + tt.url}}
		_, newer, err := checkUpdate(t.Context(), m)
		if newer != tt.newer || (err != nil) != tt.err {
			t.Errorf("%s at %s: newer %v, err %v", tt.url, tt.version, newer, err)
		}
	}
}

func TestReleaseNotes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/a/b/releases" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`[
			{"tag_name": "1.2.0", "body": "## Fixes\r\n- fix two\r\n[install](https://hitman-resources.netlify.app/smf-install-link/x)"},
			{"tag_name": "1.1.0", "body": "intro\n## Fixes\n- fix one\n## Features\n- feature"},
			{"tag_name": "1.0.0", "body": "## Fixes\n- old"}
		]`))
	}))
	defer srv.Close()
	githubAPI = srv.URL
	defer func() { githubAPI = "https://api.github.com" }()
	m := &Mod{Manifest: &Manifest{Version: "1.0.0", UpdateCheck: "https://github.com/a/b/releases/latest/download/updates.json"}}
	got, err := releaseNotes(t.Context(), m)
	if err != nil {
		t.Fatal(err)
	}
	want := "intro\n## Fixes\n- fix one\n- fix two\n## Features\n- feature"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	m.Manifest.UpdateCheck = "https://example.com/updates.json"
	if got, _ := releaseNotes(t.Context(), m); got != "" {
		t.Errorf("non-GitHub mod: %q", got)
	}
}
