package main

import (
	"bytes"
	"os"
	"testing"
)

func TestPDRoundTrip(t *testing.T) {
	plain := []byte("@partition name=super parent=none type=standard patchlevel=4\r\nabc")
	got, err := decryptPD(encryptPD(plain))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(got, plain) || len(got)%8 != 0 {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestDecryptGamePD(t *testing.T) {
	b, err := os.ReadFile("../SMF/cleanPackageDefinition.txt")
	if err != nil {
		t.Skip("no clean packagedefinition next to the repo")
	}
	plain, err := decryptPD(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(plain, []byte("@partition name=super")) {
		t.Fatal("unexpected plaintext")
	}
}
