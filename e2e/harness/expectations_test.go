//go:build e2e

package harness

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int // -1 less, 0 equal, 1 greater
	}{
		{"1.6.6", "1.8.0", -1},
		{"1.7.5", "1.8.0", -1},
		{"1.8.4", "1.8.0", 1},
		{"1.8.0", "1.8.0", 0},
		{"1.8", "1.8.0", 0},    // missing components read as 0
		{"1.10.0", "1.9.0", 1}, // numeric, not lexicographic
		{"2.0.0", "1.99.99", 1},
	}
	for _, c := range cases {
		got := compareVersions(c.a, c.b)
		switch {
		case c.want < 0 && got >= 0, c.want > 0 && got <= 0, c.want == 0 && got != 0:
			t.Errorf("compareVersions(%q, %q) = %d, want sign %d", c.a, c.b, got, c.want)
		}
	}
}

// TestExpectationForMTLSNoCA pins the window that made this table exist:
// the divergence is real below 1.8.0 and gone at and above it.
func TestExpectationForMTLSNoCA(t *testing.T) {
	const name = "TestMTLSEnabledWithNoCARejectsConnections"

	for _, v := range []string{"1.6.6", "1.7.5"} {
		if got := ExpectationFor(name, v); got == nil {
			t.Errorf("ExpectationFor(%s, %q) = nil, want the known-fail entry", name, v)
		} else if got.Outcome != OutcomeKnownFail {
			t.Errorf("ExpectationFor(%s, %q).Outcome = %q, want %q", name, v, got.Outcome, OutcomeKnownFail)
		}
	}

	for _, v := range []string{"1.8.0", "1.8.4", "1.9.0"} {
		if got := ExpectationFor(name, v); got != nil {
			t.Errorf("ExpectationFor(%s, %q) = %v, want nil: the divergence is fixed from 1.8.0", name, v, got)
		}
	}
}

// TestExpectationForUndeclaredVersionMatchesNothing keeps an undeclared
// release on the strict default. An exemption must be asked for.
func TestExpectationForUndeclaredVersionMatchesNothing(t *testing.T) {
	for _, e := range Expectations {
		if got := ExpectationFor(e.Test, ""); got != nil {
			t.Errorf("ExpectationFor(%s, \"\") = %v, want nil", e.Test, got)
		}
	}
}

func TestExpectationForUnlistedTestIsNil(t *testing.T) {
	if got := ExpectationFor("TestNoSuchTestExists", "1.6.6"); got != nil {
		t.Errorf("ExpectationFor of an unlisted test = %v, want nil", got)
	}
}

// TestExpectationsNameRealTests is the guard against orphaned entries.
//
// Call sites pass t.Name(), so a renamed test silently stops matching its
// entry and the exemption becomes invisible rather than loud. This walks
// the suites and fails if an entry names a test that no longer exists.
func TestExpectationsNameRealTests(t *testing.T) {
	funcRe := regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)
	found := map[string]string{}

	root := filepath.Join("..", "suites")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range funcRe.FindAllStringSubmatch(string(src), -1) {
			found[m[1]] = path
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(found) == 0 {
		t.Fatalf("found no test functions under %s -- the guard cannot work, fix the walk before trusting it", root)
	}

	for _, e := range Expectations {
		if _, ok := found[e.Test]; !ok {
			t.Errorf("expectation names %q, which no longer exists under %s.\n"+
				"Either the test was renamed (update the entry) or deleted (remove it).\n"+
				"Reason on the stale entry: %s", e.Test, root, e.Reason)
		}
	}
}

// TestExpectationsAreJustified keeps the table from decaying into a list of
// muted tests: every entry must say what the release does and where it is
// tracked.
func TestExpectationsAreJustified(t *testing.T) {
	for _, e := range Expectations {
		if e.Outcome != OutcomeKnownFail {
			t.Errorf("%s: outcome %q is not a supported outcome", e.Test, e.Outcome)
		}
		if len(e.Reason) < 40 {
			t.Errorf("%s: Reason is too thin to act on: %q", e.Test, e.Reason)
		}
		if e.Ref == "" {
			t.Errorf("%s: Ref is empty; an exemption needs a tracking record", e.Test)
		}
		if e.MinInclusive == "" && e.MaxExclusive == "" {
			t.Errorf("%s: unbounded entry. A divergence with no release window is not "+
				"release-dependent and does not belong in this table", e.Test)
		}
	}
}
