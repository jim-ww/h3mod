package main

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

func TestLZ4RoundTrip(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 2))
	random := make([]byte, 100000)
	for i := range random {
		random[i] = byte(rnd.IntN(256))
	}
	repetitive := bytes.Repeat([]byte("abcdefgh0123"), 20000)
	mixed := append(append([]byte(nil), random[:5000]...), repetitive[:50000]...)
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"short", []byte("hello")},
		{"random", random},
		{"repetitive", repetitive},
		{"zeros", make([]byte, 300000)},
		{"mixed", mixed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := lz4Compress(tt.data)
			got, err := lz4Decode(c, len(tt.data))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tt.data) {
				t.Fatal("round trip mismatch")
			}
		})
	}
}
