package download

import (
	"testing"
	"time"
)

func TestBackoffHonoursRetryAfterAndCapsIt(t *testing.T) {
	h := NewHTTP(Options{RateLimitWait: time.Second, MaxRateLimitWait: 10 * time.Second})
	for i := 0; i < 50; i++ {
		if d := h.backoff(1, 1, 5*time.Second); d < 5*time.Second || d > 7500*time.Millisecond {
			t.Fatalf("Retry-After 5s gave a wait of %s, want 5s plus up to 50%% jitter", d)
		}
		if d := h.backoff(9, 9, time.Hour); d > 15*time.Second {
			t.Fatalf("an hour-long Retry-After waited %s, want the 10s cap plus jitter", d)
		}
		if d := h.backoff(2, 3, 0); d < 4*time.Second {
			t.Fatalf("third consecutive 429 waited %s, want at least 4s (doubling from 1s)", d)
		}
	}
}

func TestBackoffAfterOtherErrorsDoublesFromTheRetryUnit(t *testing.T) {
	h := NewHTTP(Options{RetryUnit: time.Second})
	if d := h.backoff(3, 0, 0); d < 8*time.Second || d > 12*time.Second {
		t.Errorf("third failure waited %s, want 8s plus jitter", d)
	}
	if d := h.backoff(30, 0, 0); d > 90*time.Second {
		t.Errorf("long failure run waited %s, want the 60s cap plus jitter", d)
	}
}

func TestLimiterClosesAConnectionOnABurstAndGrowsBackWhenCalm(t *testing.T) {
	l := newLimiter(10, 0, 10*time.Millisecond)
	for i := 0; i < 10; i++ {
		if !l.grow() {
			t.Fatalf("connection %d was refused below the target", i)
		}
	}
	if l.grow() {
		t.Fatal("an eleventh connection was granted")
	}
	closed, target := l.throttled()
	if !closed || target != 7 {
		t.Fatalf("throttled() = %v, %d; want the caller closed and a target of 7 (3/4 of 10)", closed, target)
	}
	time.Sleep(20 * time.Millisecond)
	l.leave()
	l.leave()
	l.leave()
	if !l.grow() {
		t.Error("no connection added back after a calm period")
	}
}
