package services

import "testing"

func TestGenerateRouteName_OperationID(t *testing.T) {
	got, reasons := generateRouteName("getUserById", "GET", "/users/{id}", 0)
	if got != "get-user-by-id" {
		t.Fatalf("want get-user-by-id, got %q", got)
	}
	if len(reasons) != 0 {
		t.Fatalf("want no reasons, got %v", reasons)
	}
}

func TestGenerateRouteName_OperationIDWithDots(t *testing.T) {
	got, _ := generateRouteName("Users.list", "GET", "/users", 0)
	if got != "users-list" {
		t.Fatalf("want users-list, got %q", got)
	}
}

func TestGenerateRouteName_FallbackToMethodPath(t *testing.T) {
	got, _ := generateRouteName("", "POST", "/v1/orders", 0)
	if got != "post-v1-orders" {
		t.Fatalf("want post-v1-orders, got %q", got)
	}
}

func TestGenerateRouteName_FallbackStripsBraces(t *testing.T) {
	got, _ := generateRouteName("", "GET", "/users/{id}/posts/{postId}", 0)
	if got != "get-users-id-posts-post-id" {
		t.Fatalf("want get-users-id-posts-post-id, got %q", got)
	}
}

func TestGenerateRouteName_SanitizeSpecialCharacters(t *testing.T) {
	got, reasons := generateRouteName("get user! id?", "GET", "/x", 0)
	if got != "get-user-id" {
		t.Fatalf("want get-user-id, got %q", got)
	}
	if !containsReason(reasons, "sanitized") {
		t.Fatalf("want sanitized reason, got %v", reasons)
	}
}

func TestGenerateRouteName_EmptyAfterSanitize_FallbackToIndex(t *testing.T) {
	got, _ := generateRouteName("???", "", "", 5)
	if got != "route-5" {
		t.Fatalf("want route-5, got %q", got)
	}
}

func TestGenerateRouteName_TruncatesTo63(t *testing.T) {
	long := "a-very-long-operation-id-that-is-clearly-going-to-exceed-the-sixty-three-character-limit-imposed-on-dns-labels"
	got, reasons := generateRouteName(long, "GET", "/x", 0)
	if len(got) > 63 {
		t.Fatalf("want <=63 chars, got %d (%q)", len(got), got)
	}
	if !containsReason(reasons, "truncated") {
		t.Fatalf("want truncated reason, got %v", reasons)
	}
}

func TestGenerateRouteName_LeadingDigitGetsPrefix(t *testing.T) {
	// DNS label can't start with digit. With "123" operationId, sanitize produces
	// "123" which starts with a digit; the pipeline must produce a name starting
	// with a letter.
	got, _ := generateRouteName("123", "POST", "/health", 0)
	if got == "" {
		t.Fatalf("got empty name")
	}
	if got[0] >= '0' && got[0] <= '9' {
		t.Fatalf("name must not start with digit, got %q", got)
	}
}

func containsReason(list []string, want string) bool {
	for _, r := range list {
		if r == want {
			return true
		}
	}
	return false
}

func TestDisambiguate_AppendsSuffix(t *testing.T) {
	used := map[string]int{}
	a := disambiguate("foo", used)
	b := disambiguate("foo", used)
	c := disambiguate("foo", used)
	if a != "foo" || b != "foo-2" || c != "foo-3" {
		t.Fatalf("want foo,foo-2,foo-3 got %s,%s,%s", a, b, c)
	}
}
