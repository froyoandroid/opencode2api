package warp

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"testing"
	"time"
)

func testPoolWith(exits ...*exit) *Pool {
	return &Pool{
		active:   exits,
		reserved: make(map[string]bool),
		cooling:  make(map[string]time.Time),
		embargo:  make(map[string]time.Time),
		changed:  make(chan struct{}),
		size:     len(exits),
		newExit: func(context.Context) (*exit, error) {
			// A replacement is registered in the background by penalize, so
			// the stub must yield a usable exit with its own egress.
			return &exit{id: "spare", egress: netip.MustParseAddr("2a09:bac5:5e01::1")}, nil
		},
	}
}

func testExit(id, addr string) *exit {
	return &exit{id: id, egress: netip.MustParseAddr(addr)}
}

// The upstream refused this egress as a spent free-tier quota. Drawing it again
// only repeats the refusal, so Acquire must skip it entirely.
func TestAcquireSkipsEmbargoedEgress(t *testing.T) {
	spent := testExit("a", "2a09:bac5:55f9:18c8::278:96")
	fresh := testExit("b", "2a09:bac5:55fb:25af::3c1:4e")
	p := testPoolWith(spent, fresh)

	p.mu.Lock()
	p.embargoExitLocked(spent.egressKey(), time.Now().Add(time.Minute))
	p.mu.Unlock()

	for i := 0; i < 4; i++ {
		lease, err := p.Acquire(t.Context())
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		if lease.exit != fresh {
			t.Fatalf("Acquire drew %s, want the exit not under quota", lease.exit.id)
		}
		lease.Release()
	}
}

// Once the window lapses the egress is usable again, so a recovered exit is not
// lost to the process's lifetime.
func TestAcquireReturnsExpiredEmbargo(t *testing.T) {
	only := testExit("a", "2a09:bac5:55f9:18c8::278:96")
	p := testPoolWith(only)
	p.mu.Lock()
	p.embargoExitLocked(only.egressKey(), time.Now().Add(-time.Second))
	p.mu.Unlock()

	lease, err := p.Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if lease.exit != only {
		t.Fatalf("Acquire drew %s, want the recovered exit", lease.exit.id)
	}
}

// Report is where the mark is learned: a 429 on the free tier names an egress
// the pool must stop drawing.
func TestReportEmbargoesOn429(t *testing.T) {
	only := testExit("a", "2a09:bac5:55f9:18c8::278:96")
	p := testPoolWith(only)
	lease := &Lease{pool: p, exit: only}
	p.Report(lease, http.StatusTooManyRequests, nil)

	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.embargoedLocked(only.egressKey(), time.Now()) {
		t.Fatal("a 429 did not embargo its egress")
	}
}

// A successful response must not embargo anything: only the quota refusal does.
func TestReportDoesNotEmbargoOnSuccess(t *testing.T) {
	only := testExit("a", "2a09:bac5:55f9:18c8::278:96")
	p := testPoolWith(only)
	p.Report(&Lease{pool: p, exit: only}, http.StatusOK, nil)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.embargoedLocked(only.egressKey(), time.Now()) {
		t.Fatal("a success embargoed its egress")
	}
}

// When every exit is spent, waiting cannot help: Acquire must report the quota
// instead of blocking until the caller's deadline.
func TestAcquireReportsQuotaWhenAllEmbargoed(t *testing.T) {
	a := testExit("a", "2a09:bac5:55f9:18c8::278:96")
	b := testExit("b", "2a09:bac5:55fb:25af::3c1:4e")
	p := testPoolWith(a, b)
	p.mu.Lock()
	p.embargoExitLocked(a.egressKey(), time.Now().Add(time.Minute))
	p.embargoExitLocked(b.egressKey(), time.Now().Add(time.Minute))
	p.mu.Unlock()

	deadline, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	_, err := p.Acquire(deadline)
	if !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("Acquire error = %v, want ErrQuotaExhausted", err)
	}
}

// One exit still free means the request is served: the fast path must not
// mistake a partial embargo for a full one.
func TestAcquireServesWhenOneExitFree(t *testing.T) {
	a := testExit("a", "2a09:bac5:55f9:18c8::278:96")
	b := testExit("b", "2a09:bac5:55fb:25af::3c1:4e")
	p := testPoolWith(a, b)
	p.mu.Lock()
	p.embargoExitLocked(a.egressKey(), time.Now().Add(time.Minute))
	p.mu.Unlock()

	lease, err := p.Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if lease.exit != b {
		t.Fatalf("Acquire drew %s, want the free exit", lease.exit.id)
	}
}
