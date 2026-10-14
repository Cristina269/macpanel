package main

import "testing"

// 回归:ring.snapshot 在环形缓冲回绕后(start+size 越界)曾 panic
func TestRingSnapshotWrapped(t *testing.T) {
	for _, extra := range []int{1, 2, ringSize / 2, ringSize - 1, ringSize, 3 * ringSize} {
		r := newRing()
		for i := 0; i < ringSize+extra; i++ {
			r.push(float64(i + 1))
		}
		got := r.snapshot() // 不能 panic
		if len(got) != ringSize {
			t.Fatalf("extra=%d: len=%d, want %d", extra, len(got), ringSize)
		}
		// 最旧 = ringSize+extra-ringSize+1 … 最新 = ringSize+extra,严格递增
		wantFirst := float64(extra + 1)
		if got[0] != wantFirst || got[ringSize-1] != float64(ringSize+extra) {
			t.Fatalf("extra=%d: got [%v…%v], want [%v…%v]",
				extra, got[0], got[ringSize-1], wantFirst, ringSize+extra)
		}
		for i := 1; i < ringSize; i++ {
			if got[i] != got[i-1]+1 {
				t.Fatalf("extra=%d: 顺序错乱 at %d", extra, i)
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
