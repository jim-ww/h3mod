package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestApplyPatch(t *testing.T) {
	tests := []struct {
		name, doc, patch, want string
		wantErr                bool
	}{
		{"add member", `{"a":1}`, `[{"op":"add","path":"/b","value":2}]`, `{"a":1,"b":2}`, false},
		{"append", `{"a":[1]}`, `[{"op":"add","path":"/a/-","value":2}]`, `{"a":[1,2]}`, false},
		{"insert", `[1,3]`, `[{"op":"add","path":"/1","value":2}]`, `[1,2,3]`, false},
		{"remove", `{"a":1,"b":2}`, `[{"op":"remove","path":"/a"}]`, `{"b":2}`, false},
		{"replace keeps order", `{"a":1,"b":2}`, `[{"op":"replace","path":"/a","value":9}]`, `{"a":9,"b":2}`, false},
		{"move", `{"a":{"x":1}}`, `[{"op":"move","from":"/a/x","path":"/y"}]`, `{"a":{},"y":1}`, false},
		{"copy", `{"a":[1]}`, `[{"op":"copy","from":"/a","path":"/b"}]`, `{"a":[1],"b":[1]}`, false},
		{"test ok", `{"a":1}`, `[{"op":"test","path":"/a","value":1}]`, `{"a":1}`, false},
		{"test fails", `{"a":1}`, `[{"op":"test","path":"/a","value":2}]`, "", true},
		{"escaped pointer", `{"a/b":1}`, `[{"op":"remove","path":"/a~1b"}]`, `{}`, false},
		{"missing", `{}`, `[{"op":"remove","path":"/x"}]`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := parseJSON([]byte(tt.doc))
			if err != nil {
				t.Fatal(err)
			}
			var ops []patchOp
			if err := json.Unmarshal([]byte(tt.patch), &ops); err != nil {
				t.Fatal(err)
			}
			err = applyPatch(doc, ops)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			var buf bytes.Buffer
			doc.encode(&buf)
			if buf.String() != tt.want {
				t.Fatalf("got %s, want %s", buf.String(), tt.want)
			}
		})
	}
}
