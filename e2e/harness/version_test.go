//go:build e2e

package harness

import "testing"

func TestL4Supported(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"1.9.1", true},
		{"1.8.4", true},
		{"1.8.0", true},
		{"v1.8.4", true},
		{"2.0.0", true},
		{"1.7.5", false},
		{"1.6.6", false},
		{"", false}, // unknown → skip (never false confidence)
		{"garbage", false},
	}
	for _, c := range cases {
		if got := L4Supported(c.v); got != c.want {
			t.Errorf("L4Supported(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}
