package services

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const maxNameLen = 63

var nameDashRunRegex = regexp.MustCompile(`-+`)

// generateRouteName produces a DNS-label-compatible route name from an OpenAPI
// operation. It returns the final name plus a slice of "reasons" describing
// transformations applied (used to populate Rename entries in the response).
//
// Pipeline:
//  1. Pick operationId if present, else fall back to "<method>-<path with {} stripped>".
//  2. Lowercase, replace non-[a-z0-9] runs with "-", trim leading/trailing "-",
//     collapse repeated "-".
//  3. If empty after sanitize → "route-<index>".
//  4. If first char is a digit, prepend "r-" (DNS labels can't start with digit).
//  5. Truncate to 63 chars (DNS label limit).
//
// Within-spec disambiguation (foo, foo-2, foo-3) is handled by disambiguate().
func generateRouteName(operationID, method, path string, index int) (string, []string) {
	reasons := []string{}

	source := operationID
	if source == "" {
		stripped := strings.NewReplacer("{", "", "}", "").Replace(path)
		source = strings.ToLower(method) + "-" + stripped
	}

	sanitized := sanitize(source)

	// Detect whether sanitisation actually changed the input beyond normal
	// camelCase splitting and lowercasing. We compute the "expected clean form"
	// by applying only the camelCase split + lowercase (no special-char replacement),
	// then compare it to the sanitized output. If they differ, special characters
	// were present and replaced, so we record "sanitized".
	naturalForm := camelToLower(source)
	if sanitized != naturalForm {
		reasons = append(reasons, "sanitized")
	}

	if sanitized == "" {
		// Use placeholder; do not record "sanitized" since the input was unusable.
		return placeholderName(index), nil
	}

	if unicode.IsDigit(rune(sanitized[0])) {
		sanitized = "r-" + sanitized
	}

	if len(sanitized) > maxNameLen {
		sanitized = strings.TrimRight(sanitized[:maxNameLen], "-")
		reasons = append(reasons, "truncated")
	}

	return sanitized, reasons
}

// camelToLower applies camelCase splitting and lowercasing without replacing
// any special characters. It is used to compute the "natural" form of the
// source so we can detect whether special-char replacement happened.
func camelToLower(s string) string {
	s = strings.TrimSpace(s)
	var expanded strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])) {
			expanded.WriteByte('-')
		}
		expanded.WriteRune(r)
	}
	return strings.ToLower(expanded.String())
}

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	// Insert a dash before each uppercase letter that follows a lowercase letter
	// or digit (camelCase splitting: getUserById → get-User-By-Id).
	var expanded strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])) {
			expanded.WriteByte('-')
		}
		expanded.WriteRune(r)
	}
	s = strings.ToLower(expanded.String())
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := nameDashRunRegex.ReplaceAllString(b.String(), "-")
	return strings.Trim(out, "-")
}

func placeholderName(index int) string {
	return "route-" + strconv.Itoa(index)
}

// disambiguate returns the input name on first call; on subsequent calls with
// the same input it returns "<name>-2", "<name>-3", etc. The used map tracks
// occurrences across calls; pass the same map for an entire spec.
func disambiguate(name string, used map[string]int) string {
	count := used[name]
	used[name] = count + 1
	if count == 0 {
		return name
	}
	return name + "-" + strconv.Itoa(count+1)
}
