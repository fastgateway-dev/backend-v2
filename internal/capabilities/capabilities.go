package capabilities

import (
	"strconv"
	"strings"
)

// Versions holds the detected cluster versions a capability reasons about.
// An empty field means that version could not be detected.
type Versions struct {
	EnvoyGateway string
	GatewayAPI   string
}

// Capability is a named, version-derived feature decision. Source selects the
// version string the capability reasons about; the capability is true when
// that version parses and is >= MinMajor.MinMinor, and DefaultWhenUnknown when
// the version is absent/unparseable.
type Capability struct {
	Name               string
	Source             func(Versions) string
	MinMajor           int
	MinMinor           int
	Exposed            bool // surface in GET /projects/{id}/capabilities?
	DefaultWhenUnknown bool
}

// Registry is the single source of truth for version-derived capabilities.
var Registry = []Capability{
	{
		Name:               "streams",
		Source:             func(v Versions) string { return v.EnvoyGateway },
		MinMajor:           1,
		MinMinor:           8,
		Exposed:            true,
		DefaultWhenUnknown: true,
	},
	{
		Name:               "l4RouteV1",
		Source:             func(v Versions) string { return v.GatewayAPI },
		MinMajor:           1,
		MinMinor:           6,
		Exposed:            false,
		DefaultWhenUnknown: true,
	},
}

// Evaluate returns every capability's value for v.
func Evaluate(v Versions) map[string]bool {
	out := make(map[string]bool, len(Registry))
	for _, c := range Registry {
		out[c.Name] = evalOne(c, v)
	}
	return out
}

// Lookup returns the capability with the given name.
func Lookup(name string) (Capability, bool) {
	for _, c := range Registry {
		if c.Name == name {
			return c, true
		}
	}
	return Capability{}, false
}

func evalOne(c Capability, v Versions) bool {
	maj, min, ok := parseMajorMinor(c.Source(v))
	if !ok {
		return c.DefaultWhenUnknown
	}
	return maj > c.MinMajor || (maj == c.MinMajor && min >= c.MinMinor)
}

// parseMajorMinor extracts major and minor ints from a semver-ish string,
// tolerating a leading "v". Returns ok=false for empty/unparseable input.
func parseMajorMinor(v string) (maj, min int, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return maj, min, true
}
