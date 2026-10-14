package main

import "testing"

// Regression: ring.snapshot used to panic after the ring buffer wrapped (start+size out of range)
func TestRingSnapshotWrapped(t *testing.T) {
	for _, extra := range []int{1, 2, ringSize / 2, ringSize - 1, ringSize, 3 * ringSize} {
		r := newRing()
		for i := 0; i < ringSize+extra; i++ {
			r.push(float64(i + 1))
		}
		got := r.snapshot() // must not panic
		if len(got) != ringSize {
			t.Fatalf("extra=%d: len=%d, want %d", extra, len(got), ringSize)
		}
		// oldest = ringSize+extra-ringSize+1 ... newest = ringSize+extra, strictly increasing
		wantFirst := float64(extra + 1)
		if got[0] != wantFirst || got[ringSize-1] != float64(ringSize+extra) {
			t.Fatalf("extra=%d: got [%v…%v], want [%v…%v]",
				extra, got[0], got[ringSize-1], wantFirst, ringSize+extra)
		}
		for i := 1; i < ringSize; i++ {
			if got[i] != got[i-1]+1 {
				t.Fatalf("extra=%d: order broken at %d", extra, i)
			}
		}
	}
}

func TestRingSnapshotPartial(t *testing.T) {
	r := newRing()
	for i := 0; i < 10; i++ {
		r.push(float64(i + 1))
	}
	got := r.snapshot()
	if len(got) != 10 || got[0] != 1 || got[9] != 10 {
		t.Fatalf("partial: got %v", got)
	}
}
