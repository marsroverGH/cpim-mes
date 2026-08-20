package service

import "testing"

func TestQualityStatusForResult(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"PASS", "OK", true},
		{"hold", "HOLD", true},
		{" FAIL ", "REJECTED", true},
		{"UNKNOWN", "", false},
	}
	for _, tt := range tests {
		got, err := QualityStatusForResult(tt.in)
		if tt.ok && err != nil {
			t.Fatalf("%q unexpected error: %v", tt.in, err)
		}
		if !tt.ok && err == nil {
			t.Fatalf("%q expected error", tt.in)
		}
		if got != tt.want {
			t.Fatalf("%q got %q want %q", tt.in, got, tt.want)
		}
	}
}
