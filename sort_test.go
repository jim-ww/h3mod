package main

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestSortLoadOrder(t *testing.T) {
	mod := func(dir, manifest string) *Mod {
		m := &Mod{Dir: dir, Manifest: &Manifest{}}
		if err := json.Unmarshal([]byte(manifest), m.Manifest); err != nil {
			t.Fatal(err)
		}
		return m
	}
	mods := []*Mod{
		mod("A", `{"id": "a", "version": "1.0.0", "loadAfter": ["c"]}`),
		mod("B", `{"id": "b", "version": "2.1.0"}`),
		mod("C", `{"id": "c", "version": "1.0.0"}`),
		mod("D", `{"id": "d", "version": "1.0.0", "loadBefore": [["b", "^2.0.0"], ["a", "<1.0.0"]]}`),
		mod("E", `{"id": "e", "version": "1.0.0", "options": [
			{"name": "x", "type": "checkbox", "enabledByDefault": true, "loadBefore": ["a"]}]}`),
	}
	tests := []struct {
		name    string
		enabled []string
		want    []string
		wantErr bool
	}{
		{"untouched without rules", []string{"B", "C"}, []string{"B", "C"}, false},
		{"after", []string{"A", "B", "C"}, []string{"B", "C", "A"}, false},
		{"before with range", []string{"B", "D"}, []string{"D", "B"}, false},
		{"range not satisfied", []string{"A", "D"}, []string{"A", "D"}, false},
		{"option rule", []string{"A", "E"}, []string{"E", "A"}, false},
		{"missing kept at end", []string{"Gone", "A", "C"}, []string{"C", "A", "Gone"}, false},
		{"cycle", []string{"A", "C", "F"}, nil, true},
	}
	mods = append(mods, mod("F", `{"id": "f", "loadAfter": ["a"], "loadBefore": ["c"]}`))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Enabled: tt.enabled}
			got, err := sortLoadOrder(cfg, mods, nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
