package main

import "testing"

func TestParseCPUToMilli(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"10m", 10},
		{"1", 1000},
		{"0.5", 500},
	}
	for _, tt := range tests {
		got, err := parseCPUToMilli(tt.in)
		if err != nil {
			t.Fatalf("parseCPUToMilli(%q): %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("parseCPUToMilli(%q)=%d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestParseMemoryToBytes(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"64Mi", 64 * 1024 * 1024},
		{"1Gi", 1024 * 1024 * 1024},
		{"1000", 1000},
	}
	for _, tt := range tests {
		got, err := parseMemoryToBytes(tt.in)
		if err != nil {
			t.Fatalf("parseMemoryToBytes(%q): %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("parseMemoryToBytes(%q)=%d, want %d", tt.in, got, tt.want)
		}
	}
}
