package gateway

import (
	"testing"
	"time"
)

// The upstream refusal captured from https://opencode.ai/inference.
const freeUsageBody = `{"type":"error","error":{"type":"FreeUsageLimitError","message":"Free usage limit reached. Free models work in OpenCode (https://opencode.ai); subscribe to OpenCode Go to keep going."}}`

func TestFreeUsageWindowClassifiesRefusal(t *testing.T) {
	window, ok := freeUsageWindow(429, []byte(freeUsageBody), "35723")
	if !ok || window != 35723*time.Second {
		t.Fatalf("window = %v, ok = %v; want the upstream Retry-After", window, ok)
	}
	// A refusal without a deadline is still a closed window.
	if window, ok := freeUsageWindow(429, []byte(freeUsageBody), ""); !ok || window != 0 {
		t.Fatalf("missing Retry-After: window = %v, ok = %v", window, ok)
	}
}

func TestFreeUsageWindowIgnoresOtherFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		retry  string
	}{
		{"per-request rate limit", 429, `{"error":{"message":"rate limit"}}`, "30"},
		{"model unavailable", 400, `{"error":{"message":"Model is unavailable."}}`, ""},
		{"server error", 500, freeUsageBody, "30"},
	}
	for _, c := range cases {
		if _, ok := freeUsageWindow(c.status, []byte(c.body), c.retry); ok {
			t.Errorf("%s: classified as a closed free window", c.name)
		}
	}
}
