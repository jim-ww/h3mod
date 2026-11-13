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
