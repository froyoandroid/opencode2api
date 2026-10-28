package gateway

import (
	"net/http"
	"testing"
	"time"
)

func testExit(index int) *proxyTransport {
	proxy := &proxyTransport{index: index, name: "exit"}
	proxy.healthy.Store(true)
	return proxy
}

func testPool(exits ...*proxyTransport) *anonymousNodes {
	p := &anonymousNodes{cooldown: time.Minute}
	for _, proxy := range exits {
		p.nodes = append(p.nodes, &anonymousNode{proxy: proxy})
	}
	return p
}

func quotaResponse(retryAfter string) *http.Response {
	resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}}
	if retryAfter != "" {
		resp.Header.Set("Retry-After", retryAfter)
	}
	return resp
}

// The free tier's Retry-After is the seconds left in its UTC-day bucket, which
// is many hours. A clamp would walk the exit back into the same refusal.
func TestMarkFailureHonoursFullRetryAfter(t *testing.T) {
	p := testPool(testExit(0))
	before := time.Now()
	p.MarkFailure(p.nodes[0], quotaResponse("18912"), nil)
	cooling := time.Until(time.Unix(0, p.nodes[0].cooldownUntil.Load()))
	if cooling < 18900*time.Second || cooling > 18920*time.Second {
		t.Fatalf("cooldown = %s, want the upstream 18912s honoured in full", cooling)
	}
	if !p.nodes[0].quotaCooldown.Load() {
		t.Fatal("a Retry-After cooldown was not marked as a quota cooldown")
	}
	if cooling > time.Until(before.Add(24*time.Hour)) {
		t.Fatal("cooldown ran past a day")
	}
}

// A quota cooldown is the upstream's answer about this egress. Restoring a
// recovered proxy route must not erase it.
func TestRestoreProxyKeepsQuotaCooldown(t *testing.T) {
	proxy := testExit(0)
	p := testPool(proxy)
	p.MarkFailure(p.nodes[0], quotaResponse("18000"), nil)
	if cleared := p.RestoreProxy(proxy); cleared != 0 {
		t.Fatalf("RestoreProxy cleared %d quota cooldowns", cleared)
	}
	if p.nodes[0].cooldownUntil.Load() == 0 {
		t.Fatal("quota cooldown was cleared by a proxy restore")
	}
	// An ordinary cooldown is still restorable.
	other := testExit(1)
	p.nodes = append(p.nodes, &anonymousNode{proxy: other})
	p.MarkFailure(p.nodes[1], quotaResponse(""), nil)
	if cleared := p.RestoreProxy(other); cleared != 1 {
		t.Fatalf("RestoreProxy cleared %d ordinary cooldowns, want 1", cleared)
	}
}

// A success is the proof the exit answers again: it clears the quota mark.
func TestMarkSuccessClearsQuotaCooldown(t *testing.T) {
	p := testPool(testExit(0))
	p.MarkFailure(p.nodes[0], quotaResponse("18000"), nil)
	p.MarkSuccess(p.nodes[0])
	if p.nodes[0].cooldownUntil.Load() != 0 || p.nodes[0].quotaCooldown.Load() {
		t.Fatal("success left the exit cooling or quota-marked")
	}
}

// The cursor's fallback must never hand back a cooling exit, or the cooldown
// is spent again inside the same request.
func TestCursorSkipsCoolingExitInFallback(t *testing.T) {
	p := testPool(testExit(0), testExit(1))
	p.MarkFailure(p.nodes[0], quotaResponse("3600"), nil)
	cursor := p.CursorFor("affinity")
	seen := 0
	for {
		node := cursor.Next()
		if node == nil {
			break
		}
		if node == p.nodes[0] {
			t.Fatal("cursor returned a cooling exit")
		}
		seen++
		if seen > 4 {
			t.Fatal("cursor looped")
		}
	}
	if seen != 1 {
		t.Fatalf("cursor returned %d nodes, want only the healthy one", seen)
	}
}

// Expired cooldowns become usable again without a restart.
func TestCursorReturnsExpiredCooldown(t *testing.T) {
	p := testPool(testExit(0))
	p.nodes[0].cooldownUntil.Store(time.Now().Add(-time.Second).UnixNano())
	c := p.CursorFor("x")
	if node := c.Next(); node != p.nodes[0] {
		t.Fatal("an expired cooldown was still skipped")
	}
}

// A retry pass opens on the next exit instead of the one that just refused.
func TestCursorForOffsetShiftsStart(t *testing.T) {
	exits := []*proxyTransport{testExit(0), testExit(1), testExit(2)}
	p := testPool(exits...)
	firstCursor := p.CursorFor("affinity")
	first := firstCursor.Next()
	secondCursor := p.CursorForOffset("affinity", 1)
	second := secondCursor.Next()
	if first == nil || second == nil {
		t.Fatal("cursor returned no node")
	}
	if first == second {
		t.Fatal("offset 1 started on the same exit")
	}
	backCursor := p.CursorForOffset("affinity", -1)
	if back := backCursor.Next(); back == nil {
		t.Fatal("negative offset returned no node")
	}
}
