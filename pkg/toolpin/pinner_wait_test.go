package toolpin

import (
	"testing"
	"time"
)

// Wait returns once no comparison is in progress, and gives up after its
// timeout when one is.
func TestPinnerWait(t *testing.T) {
	var nilPinner *Pinner
	if !nilPinner.Wait(time.Millisecond) {
		t.Fatal("a nil pinner has nothing in progress")
	}

	p := &Pinner{}
	if !p.Wait(time.Millisecond) {
		t.Fatal("an idle pinner must not wait")
	}

	p.inflight.Add(1)
	start := time.Now()
	if p.Wait(30 * time.Millisecond) {
		t.Fatal("Wait returned true with a comparison in progress")
	}
	if time.Since(start) < 30*time.Millisecond {
		t.Fatal("Wait gave up before its timeout")
	}

	go func() {
		time.Sleep(20 * time.Millisecond)
		p.inflight.Add(-1)
	}()
	if !p.Wait(2 * time.Second) {
		t.Fatal("Wait must return once the comparison ends")
	}
}
