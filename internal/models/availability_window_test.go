package models

import (
	"testing"
	"time"
)

// A closed free window is one shared upstream fact, not eight model failures:
// recording it for one model must defer every other automatic model too.
func TestRecordWindowDefersEveryAutomaticModel(t *testing.T) {
	s, err := AvailabilityStore("")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 14, 0, 0, 0, time.UTC)
	for _, model := range []string{"a-free", "b-free", "c-free", "manual-free"} {
		s.items[model] = Availability{Model: model, NextCheck: now, Generation: 7}
	}
	manual := s.items["manual-free"]
	manual.Manual = true
	s.items["manual-free"] = manual

	if err := s.RecordWindow("a-free", 7, false, "free_usage_window", "anonymous", now, EffortProbe{}, 10*time.Hour); err != nil {
		t.Fatal(err)
	}
	reopen := now.Add(10 * time.Hour)
	for _, model := range []string{"a-free", "b-free", "c-free"} {
		got := s.items[model]
		if !got.NextCheck.Equal(reopen) {
			t.Errorf("%s next check = %s, want %s", model, got.NextCheck, reopen)
		}
		if got.Disabled {
			t.Errorf("%s disabled by a window close: %+v", model, got)
		}
	}
	if got := s.items["manual-free"].NextCheck; !got.Equal(now) {
		t.Errorf("manual model next check = %s, want untouched %s", got, now)
	}
}

// Without the upstream window a failure keeps the ordinary hourly schedule.
func TestRecordWindowWithoutWindowKeepsHourlyRetry(t *testing.T) {
	s, err := AvailabilityStore("")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 14, 0, 0, 0, time.UTC)
	s.items["a-free"] = Availability{Model: "a-free", NextCheck: now, Generation: 3}
	s.items["b-free"] = Availability{Model: "b-free", NextCheck: now, Generation: 3}
	if err := s.RecordWindow("a-free", 3, false, "http_429", "anonymous", now, EffortProbe{}, 0); err != nil {
		t.Fatal(err)
	}
	if got := s.items["a-free"].NextCheck; !got.Equal(now.Add(FreeModelCheckInterval)) {
		t.Errorf("probed model next check = %s, want hourly", got)
	}
	if got := s.items["b-free"].NextCheck; !got.Equal(now) {
		t.Errorf("unprobed model moved to %s", got)
	}
}

// A free model the registry has never written must not be probed while the
// tier is closed: it has no schedule, so it inherits the shared reopen time.
func TestUnknownModelInheritsTierWindow(t *testing.T) {
	s, err := AvailabilityStore("")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 14, 0, 0, 0, time.UTC)
	s.items["probed-free"] = Availability{Model: "probed-free", NextCheck: now, Generation: 1}
	if err := s.RecordWindow("probed-free", 1, false, "free_usage_window", "anonymous", now, EffortProbe{}, 10*time.Hour); err != nil {
		t.Fatal(err)
	}
	reopen := now.Add(10 * time.Hour)
	if got := s.Get("never-probed-free").NextCheck; !got.Equal(reopen) {
		t.Errorf("unknown model next check = %s, want the tier reopen %s", got, reopen)
	}
	// An ordinary failure written inside the window is deferred to the same
	// reopen: the tier, not the model, is what is closed.
	if err := s.RecordWindow("second-free", 1, false, "http_429", "anonymous", now.Add(time.Hour), EffortProbe{}, 0); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("second-free").NextCheck; !got.Equal(reopen) {
		t.Errorf("model written inside the window: next check = %s, want the tier reopen %s", got, reopen)
	}
}
