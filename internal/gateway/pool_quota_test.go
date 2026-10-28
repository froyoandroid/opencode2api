package gateway

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// quotaResponse builds the free tier's shared-bucket rejection with a
// Retry-After the caller controls.
func quotaResponse(retryAfter string) *http.Response {
	body := `{"type":"error","error":{"type":"FreeUsageLimitError","message":"Free usage limit reached."}}`
	header := http.Header{}
	if retryAfter != "" {
		header.Set("Retry-After", retryAfter)
	}
	return &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// midnight returns a reset instant inside the next UTC day, offset from
// midnight so several reports land in the same day but at different seconds.
func midnight(offset time.Duration) time.Time {
	now := time.Now().UTC()
	return now.Truncate(24 * time.Hour).Add(24*time.Hour + offset)
}

// Exits disagree about the exact reset second — a direct request counts down
// to midnight while a fresh WARP exit is handed a later penalty — but they all
// reopen inside the same UTC day. The lane must read those as one window and
// stop leasing once enough exits agree.
func TestQuotaWindowStopsAfterEnoughSharedExits(t *testing.T) {
	p := &anonymousNodes{}
	for attempt := 1; attempt < sharedBucketExits; attempt++ {
		if tryAnother := p.NoteQuotaClosed(midnight(time.Duration(attempt) * 7 * time.Minute)); !tryAnother {
			t.Fatalf("attempt %d: another exit refused while the bucket was still being confirmed", attempt)
		}
		if _, closed := p.QuotaClosedUntil(time.Now()); closed {
			t.Fatalf("attempt %d: lane closed before %d exits agreed", attempt, sharedBucketExits)
		}
	}
	latest := midnight(20 * time.Minute)
	p.NoteQuotaClosed(latest)
	reopens, closed := p.QuotaClosedUntil(time.Now())
	if !closed {
		t.Fatalf("lane stayed open after %d exits reported the same day", sharedBucketExits)
	}
	if !reopens.Equal(latest) {
		t.Fatalf("reopen = %v, want the latest instant %v", reopens, latest)
	}
}

// A report from a different day is a different window, not more evidence for
// the one already recorded, so it restarts confirmation instead of pausing the
// lane on stale agreement.
func TestQuotaWindowFromLaterDayRestartsConfirmation(t *testing.T) {
	p := &anonymousNodes{}
	for range sharedBucketExits {
		p.NoteQuotaClosed(midnight(time.Minute))
	}
	if _, closed := p.QuotaClosedUntil(time.Now()); !closed {
		t.Fatal("lane should be closed after enough exits agree")
	}
	later := midnight(24*time.Hour + time.Hour)
	if tryAnother := p.NoteQuotaClosed(later); !tryAnother {
		t.Fatal("a window on a later day must restart confirmation")
	}
	if _, closed := p.QuotaClosedUntil(time.Now()); closed {
		t.Fatal("lane stayed closed on an unconfirmed later window")
	}
	if reopens, _ := p.QuotaClosedUntil(time.Now()); !reopens.Equal(later) {
		t.Fatalf("reopen = %v, want the later instant %v", reopens, later)
	}
}

// The window is a promise about the future, so it lapses once the day ends.
func TestQuotaWindowLapsesAfterItsDay(t *testing.T) {
	p := &anonymousNodes{}
	until := midnight(time.Minute)
	for range sharedBucketExits {
		p.NoteQuotaClosed(until)
	}
	if _, closed := p.QuotaClosedUntil(until.Add(24 * time.Hour)); closed {
		t.Fatal("lane stayed closed past the recorded day")
	}
}

// An exit that succeeds proves the bucket reopened, whatever was recorded.
func TestClearQuotaReopensLane(t *testing.T) {
	p := &anonymousNodes{}
	until := midnight(time.Minute)
	for range sharedBucketExits {
		p.NoteQuotaClosed(until)
	}
	p.ClearQuota()
	if _, closed := p.QuotaClosedUntil(time.Now()); closed {
		t.Fatal("lane stayed closed after an exit succeeded")
	}
	if tryAnother := p.NoteQuotaClosed(until); !tryAnother {
		t.Fatal("a cleared window must start a fresh confirmation round")
	}
}

// Only the free tier's quota envelope with a reset header schedules a window;
// generic rate limits must not pause the lane.
func TestObserveQuotaWindowIgnoresOtherRejections(t *testing.T) {
	p := &anonymousNodes{}
	if _, ok := p.observeQuotaWindow(quotaResponse("")); ok {
		t.Fatal("a 429 without Retry-After must not close the lane")
	}
	plain := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":"slow down"}`))}
	plain.Header.Set("Retry-After", "120")
	if _, ok := p.observeQuotaWindow(plain); ok {
		t.Fatal("a generic rate limit is not the free tier's quota")
	}
	if _, closed := p.QuotaClosedUntil(time.Now()); closed {
		t.Fatal("lane closed on a rejection that is not the shared bucket")
	}
}
