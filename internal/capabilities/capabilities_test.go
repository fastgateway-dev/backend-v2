package capabilities

import "testing"

func TestEvaluate(t *testing.T) {
	cases := []struct {
		name     string
		versions Versions
		want     map[string]bool
	}{
		{"eg19 gw16 -> both v1/streams", Versions{"1.9.1", "1.6.1"}, map[string]bool{"streams": true, "l4RouteV1": true}},
		{"eg18 gw15 -> streams yes, l4 alpha", Versions{"1.8.4", "1.5.1"}, map[string]bool{"streams": true, "l4RouteV1": false}},
		{"eg17 gw14 -> neither", Versions{"1.7.5", "1.4.1"}, map[string]bool{"streams": false, "l4RouteV1": false}},
		{"leading v tolerated", Versions{"v1.9.1", "v1.6.1"}, map[string]bool{"streams": true, "l4RouteV1": true}},
		{"future major -> true", Versions{"2.0.0", "2.0.0"}, map[string]bool{"streams": true, "l4RouteV1": true}},
		{"empty -> defaults (both true)", Versions{"", ""}, map[string]bool{"streams": true, "l4RouteV1": true}},
		{"garbage -> defaults (both true)", Versions{"latest", "dev"}, map[string]bool{"streams": true, "l4RouteV1": true}},
		{"bare major -> defaults", Versions{"1", "1"}, map[string]bool{"streams": true, "l4RouteV1": true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Evaluate(c.versions)
			for k, want := range c.want {
				if got[k] != want {
					t.Errorf("%s: cap %q = %v, want %v", c.name, k, got[k], want)
				}
			}
		})
	}
}

func TestLookup(t *testing.T) {
	if c, ok := Lookup("streams"); !ok || !c.Exposed {
		t.Fatalf("streams must exist and be Exposed; got %+v ok=%v", c, ok)
	}
	if c, ok := Lookup("l4RouteV1"); !ok || c.Exposed {
		t.Fatalf("l4RouteV1 must exist and be internal (not Exposed); got %+v ok=%v", c, ok)
	}
	if _, ok := Lookup("nope"); ok {
		t.Fatal("unknown capability must return ok=false")
	}
}
