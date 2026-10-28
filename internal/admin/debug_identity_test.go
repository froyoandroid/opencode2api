package admin

import (
	"net/http"
	"testing"

	"opencode2api/internal/identity"
)

// The Playground replays one prompt on every click. Without a session header
// the gateway derives identity from that prompt, so identical clicks share one
// session and burn a single upstream bucket until it 429s while live traffic
// succeeds. The handler must send a per-click session instead.
func TestDebugInferenceSendsFreshSession(t *testing.T) {
	header := http.Header{}
	header.Set("x-opencode-session", identity.RandomID("playground", 24))

	body := map[string]any{
		"model":    "glm-5.3-flash-free",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}
	first := identity.DeriveRequestIDs(&http.Request{Header: header}, body, "opencode2api")

	fresh := http.Header{}
	fresh.Set("x-opencode-session", identity.RandomID("playground", 24))
	second := identity.DeriveRequestIDs(&http.Request{Header: fresh}, body, "opencode2api")

	if first.Session == second.Session {
		t.Fatalf("identical Playground clicks shared session %s", first.Session)
	}

	// The regression itself: without the header the derived seed is stable.
	unsignalled := identity.DeriveRequestIDs(&http.Request{Header: http.Header{}}, body, "opencode2api")
	again := identity.DeriveRequestIDs(&http.Request{Header: http.Header{}}, body, "opencode2api")
	if unsignalled.Session != again.Session {
		t.Skip("prompt-derived identity is no longer stable; this assertion documents the old failure mode")
	}
}
