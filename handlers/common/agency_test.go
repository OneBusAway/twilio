package common

import "testing"

func TestAgencyPrefix(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"1_75403", "1"},
		{"40_75403", "40"},
		{"75403", "none"},
		{"_75403", "none"},
		{"", "none"},
	}
	for _, tc := range cases {
		if got := AgencyPrefix(tc.input); got != tc.want {
			t.Errorf("AgencyPrefix(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
