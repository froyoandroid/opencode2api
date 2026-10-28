package gateway

import (
	"testing"
	"time"

	"opencode2api/internal/identity"
)

// probeIDs mints the identity a probe of model carries, mirroring
// probeFreeModel. Keeping it in one place lets the test assert the property
// that matters without standing up a gateway.
func probeIDs(model string) identity.RequestIDs {
	return identity.RequestIDs{
		Session: identity.CanonicalSessionID("availability:" + model + ":" + identity.RandomID("p", 8)),
		Project: identity.RandomProjectID(),
	}
}

// A probe must not reuse a bucket. The free tier ledges quota per project id
// and egress and treats a session id as one continuous conversation, so two
// consecutive probes for the same model that shared either signal would spend
// one bucket and then report 429 while live traffic succeeded.
func TestProbeIdentityVariesPerSweep(t *testing.T) {
	first := probeIDs("glm-5.3-flash-free")
	// CanonicalSessionID derives part of the id from the clock, so a second
	// probe in the same instant still has to differ through its random part.
	time.Sleep(time.Millisecond)
	second := probeIDs("glm-5.3-flash-free")

	if first.Project == second.Project {
		t.Fatalf("two probes shared project %s: one shared bucket", first.Project)
	}
	if first.Session == second.Session {
		t.Fatalf("two probes shared session %s: one continuous conversation", first.Session)
	}
	if len(first.Project) != 40 {
		t.Fatalf("project id length = %d, want the 40-hex shape the CLI sends", len(first.Project))
	}
}

// Different models must not share a session either: the sweep probes every
// free model, and one bucket per model is what the per-model session used to
// give away as a single lineage.
func TestProbeIdentityVariesPerModel(t *testing.T) {
	a := probeIDs("glm-5.3-flash-free")
	b := probeIDs("big-pickle")
	if a.Session == b.Session {
		t.Fatalf("probes for different models shared session %s", a.Session)
	}
	if a.Project == b.Project {
		t.Fatalf("probes for different models shared project %s", a.Project)
	}
}
