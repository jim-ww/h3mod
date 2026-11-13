package main

import (
	"bytes"
	"testing"
)

func TestGameORESRoundTrip(t *testing.T) {
	idx, err := loadIndex("../Runtime")
	if err != nil {
		t.Skip("no game files: ", err)
	}
	data := func(h uint64) []byte {
		t.Helper()
		r, err := idx.lookup(h)
		if err != nil {
			t.Fatal(err)
		}
		d, err := r.Data()
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	u := data(unlockablesORES)
	j, err := parseUnlockablesORES(u)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buildUnlockablesORES(j), u) {
		t.Error("unlockables ORES does not round trip")
	}
	c := data(contractsORES)
	entries, err := parseContractsORES(c)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buildContractsORES(entries), c) {
		t.Error("contracts ORES does not round trip")
	}
	t.Logf("%d bytes of unlockables JSON, %d contracts", len(j), len(entries))
}
