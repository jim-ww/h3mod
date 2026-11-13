package main

import "testing"

func TestEvalCondition(t *testing.T) {
	vars := map[string]any{"config": map[string]any{
		"loadOrder":  []string{"Atampy26.SomeOtherMod", "Other"},
		"modOptions": map[string]any{"Other": []string{"Group:A"}},
		"platform":   "steam",
	}}
	tests := []struct {
		expr    string
		want    bool
		wantErr bool
	}{
		{`"Atampy26.SomeOtherMod" in config.loadOrder`, true, false},
		{`"Missing" in config.loadOrder`, false, false},
		{`"Missing" not in config.loadOrder`, true, false},
		{`not ("Missing" in config.loadOrder)`, true, false},
		{`'Other' in config.loadOrder and "Group:A" in config.modOptions.Other`, true, false},
		{`config.platform == "steam" or false`, true, false},
		{`config.nothing.deeper`, false, false},
		{`1 + 2`, false, true},
	}
	for _, tt := range tests {
		got, err := evalCondition(tt.expr, vars)
		if (err != nil) != tt.wantErr || (err == nil && got != tt.want) {
			t.Errorf("%s = %v, %v; want %v (error %v)", tt.expr, got, err, tt.want, tt.wantErr)
		}
	}
}
