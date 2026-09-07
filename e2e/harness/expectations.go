//go:build e2e

package harness

import (
	"fmt"
	"strconv"
	"strings"
)

// Outcome is what a test is expected to observe on a given Envoy Gateway
// release, when that differs from the default.
type Outcome string

// OutcomeKnownFail means the behaviour under test is BROKEN on the matched
// releases, and the test must assert the broken behaviour rather than skip.
//
// Asserting the divergence is what makes an entry self-retiring: when the
// upstream release fixes it, the inverted assertion fails and names the
// entry to delete. A skip would go stale silently and could outlive the
// defect by years.
const OutcomeKnownFail Outcome = "known-fail"

// Expectation records one place where a test's expected result depends on
// which Envoy Gateway release is under test.
//
// Entries are EXCEPTIONS. The default for every test on every release is
// "passes", and listing the ~186 tests against each supported release would
// produce a table nobody maintains. Only divergences appear here.
type Expectation struct {
	// Test is the exact Go test name, as reported by t.Name(). Call sites
	// pass t.Name() rather than a literal, and TestExpectationsNameRealTests
	// checks this direction, so a rename cannot orphan an entry.
	Test string

	Outcome Outcome

	// MinInclusive and MaxExclusive bound the releases this applies to.
	// Either may be empty for an open end. A release is matched when
	// MinInclusive <= version < MaxExclusive.
	MinInclusive string
	MaxExclusive string

	// Reason states what the release actually does, in terms a reader can
	// check against a CI log. Ref points at the tracking record.
	Reason string
	Ref    string
}

// Expectations is the complete set of known release-dependent divergences.
var Expectations = []Expectation{
	{
		Test:         "TestMTLSEnabledWithNoCARejectsConnections",
		Outcome:      OutcomeKnownFail,
		MaxExclusive: "1.8.0",
		Reason: "Envoy Gateway does not reconcile a ClientTrafficPolicy whose " +
			"ClientValidation carries an empty caCertificateRefs: the policy gets no " +
			"status.conditions at all, and the domain keeps serving 200 to requests " +
			"presenting no client certificate. Strict mTLS with zero CAs is a silent " +
			"no-op on these releases -- it fails OPEN. From 1.8.0 the same manifest " +
			"fails closed with HTTP 500. Measured on 1.6.6, 1.7.5 and 1.8.4 in " +
			"actions run 34087475003.",
		Ref: "https://github.com/fastgateway-dev/backend-v2/actions/runs/34087475003",
	},
}

// ExpectationFor returns the entry covering testName on egVersion, or nil
// when the test is expected to pass.
//
// An empty egVersion matches nothing: a cluster whose release nobody
// declared gets the strict default rather than an exemption it did not ask
// for.
func ExpectationFor(testName, egVersion string) *Expectation {
	if egVersion == "" {
		return nil
	}
	for i := range Expectations {
		e := &Expectations[i]
		if e.Test != testName {
			continue
		}
		if e.MinInclusive != "" && compareVersions(egVersion, e.MinInclusive) < 0 {
			continue
		}
		if e.MaxExclusive != "" && compareVersions(egVersion, e.MaxExclusive) >= 0 {
			continue
		}
		return e
	}
	return nil
}

// String renders an entry for a test log, so a run that takes the
// known-fail arm says which release, what it does, and where it is tracked.
func (e *Expectation) String() string {
	return fmt.Sprintf("known-fail (%s): %s [%s]", e.window(), e.Reason, e.Ref)
}

func (e *Expectation) window() string {
	switch {
	case e.MinInclusive == "" && e.MaxExclusive == "":
		return "all releases"
	case e.MinInclusive == "":
		return "< " + e.MaxExclusive
	case e.MaxExclusive == "":
		return ">= " + e.MinInclusive
	default:
		return e.MinInclusive + " <= v < " + e.MaxExclusive
	}
}

// compareVersions orders dotted numeric releases: negative when a < b, zero
// when equal, positive when a > b. Missing components read as 0, so "1.8"
// and "1.8.0" compare equal. A non-numeric component sorts as 0 rather than
// erroring; the releases this compares come from a CI matrix of plain
// numeric tags, and the guard test pins the behaviour that matters.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		if d := atoiOrZero(part(as, i)) - atoiOrZero(part(bs, i)); d != 0 {
			return d
		}
	}
	return 0
}

func part(parts []string, i int) string {
	if i < len(parts) {
		return parts[i]
	}
	return ""
}

func atoiOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
