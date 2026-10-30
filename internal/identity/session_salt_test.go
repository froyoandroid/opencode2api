package identity

import (
	"net/http"
	"testing"
)

func signalRequest(session string) (*http.Request, map[string]any) {
	r, _ := http.NewRequest(http.MethodPost, "http://gateway.local/v1/chat/completions", nil)
	if session != "" {
		r.Header.Set("x-opencode-session", session)
	}
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "same opening turn"},
		},
	}
	return r, body
}

// A restart must not re-present a session the free tier has already charged,
// so the same conversation under two process salts derives two ids.
func TestSessionIDDiffersAcrossProcessSalts(t *testing.T) {
	r, body := signalRequest("")
	before := deriveIDs(r, body, "scope", []byte("salt-a")).Session
	after := deriveIDs(r, body, "scope", []byte("salt-b")).Session
	if before == after {
		t.Fatalf("restart derived the same session %s: the burned id survives", before)
	}
	if !isOfficialSessionID(before) || !isOfficialSessionID(after) {
		t.Fatalf("derived sessions %q / %q are not in the shape the free tier accepts", before, after)
	}
}

// Within one process the session must stay stable, or affinity, sticky
// providers and prompt cache all break.
func TestSessionIDStableWithinOneProcess(t *testing.T) {
	firstRequest, firstBody := signalRequest("")
	secondRequest, secondBody := signalRequest("")
	first := deriveIDs(firstRequest, firstBody, "scope", []byte("salt-a")).Session
	second := deriveIDs(secondRequest, secondBody, "scope", []byte("salt-a")).Session
	if first != second {
		t.Fatalf("one process derived %s then %s for one conversation", first, second)
	}
}

// An official client session is the caller's own conversation upstream; the
// salt must not rewrite it.
func TestOfficialSessionForwardedVerbatim(t *testing.T) {
	const official = "ses_e7d96c721ffefIaGgHjKlMnOpQ"
	r, body := signalRequest(official)
	if got := deriveIDs(r, body, "scope", []byte("salt-a")).Session; got != official {
		t.Fatalf("official session rewritten to %s", got)
	}
	if got := deriveIDs(r, body, "scope", []byte("salt-b")).Session; got != official {
		t.Fatalf("official session rewritten to %s under a different salt", got)
	}
}

// A client-minted child session is forwarded unchanged even when a separate
// parent conversation signal drives the salted derivation.
func TestChildSessionForwardedVerbatim(t *testing.T) {
	const child = "ses_9a8626a2bffekXyZaBcDeFgHiJ"
	r, body := signalRequest("ses_1234567890abfeedcba987654321YX")
	r.Header.Set("x-opencode-session-id", child)
	ids := deriveIDs(r, body, "scope", []byte("salt-a"))
	if ids.ChildSession != child {
		t.Fatalf("child session rewritten to %s", ids.ChildSession)
	}
	if ids.Session == child {
		t.Fatal("parent session leaked the child identity")
	}
	if !isOfficialSessionID(ids.Session) {
		t.Fatalf("parent session %q is not in the shape the free tier accepts", ids.Session)
	}
}

// Two callers with identical conversations still must not share a session.
func TestScopeStillSeparatesCallers(t *testing.T) {
	aRequest, aBody := signalRequest("")
	bRequest, bBody := signalRequest("")
	a := deriveIDs(aRequest, aBody, "caller-a", []byte("salt"))
	b := deriveIDs(bRequest, bBody, "caller-b", []byte("salt"))
	if a.Session == b.Session {
		t.Fatalf("two callers shared session %s", a.Session)
	}
}
