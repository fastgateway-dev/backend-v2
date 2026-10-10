package repository

import (
	"strings"
	"testing"
)

func TestCapabilityListenerClause(t *testing.T) {
	c, args, ok := capabilityListenerClause("stream")
	if !ok || !strings.Contains(c, "listeners @>") || len(args) != 2 {
		t.Fatalf("stream clause wrong: %q args=%v ok=%v", c, args, ok)
	}
	joined := strings.Join(argStrings(args), " ")
	if !strings.Contains(joined, "TCP") || !strings.Contains(joined, "UDP") {
		t.Fatalf("stream args must cover TCP and UDP: %v", args)
	}
	c, args, ok = capabilityListenerClause("domain")
	if !ok || len(args) != 3 {
		t.Fatalf("domain clause wrong: %q args=%v ok=%v", c, args, ok)
	}
	joined = strings.Join(argStrings(args), " ")
	for _, p := range []string{"HTTP\"", "HTTPS", "TLS"} {
		if !strings.Contains(joined, p) {
			t.Fatalf("domain args missing %s: %v", p, args)
		}
	}
	if _, _, ok := capabilityListenerClause(""); ok {
		t.Fatal("empty capability must yield no clause")
	}
	if _, _, ok := capabilityListenerClause("bogus"); ok {
		t.Fatal("unknown capability must yield no clause")
	}
}

func argStrings(args []any) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = a.(string)
	}
	return out
}
