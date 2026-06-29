package main

import "testing"

func TestResolveMetricsPort(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"", "9119"},
		{"9119", "9119"},
		{"8000", "8000"},
		{"  9200 ", "9200"},
		{"abc", "9119"},
		{"0", "9119"},
		{"-5", "9119"},
		{"70000", "9119"},
	}
	for _, c := range cases {
		if got := resolveMetricsPort(c.raw); got != c.want {
			t.Errorf("resolveMetricsPort(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}
