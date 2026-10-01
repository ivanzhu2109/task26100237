package confcheck

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

const perfSessions = 100_000

func heapAllocMiB() float64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapAlloc) / (1 << 20)
}

// verifyConflict 根据输入独立复核一条冲突的原因是否齐全且正确。
func verifyConflict(t *testing.T, byID map[int64]*Session, cf Conflict) {
	t.Helper()
	if cf.A >= cf.B {
		t.Fatalf("conflict not ordered by ID: %+v", cf)
	}
	a, b := byID[cf.A], byID[cf.B]
	if a.StartMs >= b.EndMs || b.StartMs >= a.EndMs {
		t.Fatalf("conflict %+v without time overlap", cf)
	}
	wantRooms := 0
	if a.Room == b.Room {
		wantRooms = 1
	}
	if len(cf.Rooms) != wantRooms {
		t.Fatalf("conflict %+v room reasons wrong", cf)
	}
	set := map[string]bool{}
	for _, s := range b.Speakers {
		set[s] = true
	}
	wantSpk := 0
	for _, s := range a.Speakers {
		if set[s] {
			wantSpk++
		}
	}
	if len(cf.Speakers) != wantSpk {
		t.Fatalf("conflict %+v speaker reasons wrong, want %d shared", cf, wantSpk)
	}
}

// 稀疏场景：10 万场次、恰好 1000 对冲突，完整读取须在 5 秒内，
// 结果有序、原因齐全，且输入+状态+未交付结果的堆内存低于 256 MiB。
func TestPerfSparseFullReport(t *testing.T) {
	sessions := make([]Session, 0, perfSessions)
	for i := 1; i <= perfSessions; i++ {
		sessions = append(sessions, Session{
			ID:       int64(i),
			Room:     fmt.Sprintf("room-%d", i),
			Speakers: []string{fmt.Sprintf("spk-%d", i)},
			StartMs:  int64(i) * 100,
			EndMs:    int64(i)*100 + 50,
		})
	}
	// 植入 500 对共享房间 + 500 对共享讲者的冲突，时间窗互不重叠。
	const base = int64(2_000_000_000)
	for k := 0; k < 500; k++ {
		a := &sessions[2*k]
		b := &sessions[2*k+1]
		a.Room, b.Room = fmt.Sprintf("pair-room-%d", k), fmt.Sprintf("pair-room-%d", k)
		a.StartMs, a.EndMs = base+int64(k)*1000, base+int64(k)*1000+10
		b.StartMs, b.EndMs = base+int64(k)*1000, base+int64(k)*1000+10
	}
	for k := 0; k < 500; k++ {
		a := &sessions[1000+2*k]
		b := &sessions[1000+2*k+1]
		sp := fmt.Sprintf("pair-spk-%d", k)
		a.Speakers, b.Speakers = []string{sp}, []string{sp}
		a.StartMs, a.EndMs = base+int64(k)*1000+500, base+int64(k)*1000+510
		b.StartMs, b.EndMs = base+int64(k)*1000+500, base+int64(k)*1000+510
	}
	byID := make(map[int64]*Session, len(sessions))
	for i := range sessions {
		byID[sessions[i].ID] = &sessions[i]
	}

	before := heapAllocMiB()
	start := time.Now()
	c, err := NewChecker(sessions)
	if err != nil {
		t.Fatal(err)
	}
	it := c.Conflicts()
	count := 0
	var prevA, prevB int64
	for {
		cf, ok := it.Next()
		if !ok {
			break
		}
		if count > 0 && (cf.A < prevA || (cf.A == prevA && cf.B <= prevB)) {
			t.Fatalf("report out of order at %d: (%d,%d) after (%d,%d)", count, cf.A, cf.B, prevA, prevB)
		}
		prevA, prevB = cf.A, cf.B
		verifyConflict(t, byID, cf)
		count++
	}
	elapsed := time.Since(start)
	after := heapAllocMiB()
	if count != 1000 {
		t.Fatalf("got %d conflicts, want 1000", count)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("full sparse report took %v, want < 5s", elapsed)
	}
	if after > 256 {
		t.Fatalf("heap %.1f MiB exceeds 256 MiB", after)
	}
	t.Logf("sparse: %d conflicts in %v, heap before=%.1f MiB after=%.1f MiB", count, elapsed, before, after)
}

// 高密度场景：10 万场次全部同房间且时间重叠（约 50 亿对冲突），
// 前 100 条须在 5 秒内交付，内容有序且正确；内存同样受限。
func TestPerfDenseFirstBatch(t *testing.T) {
	sessions := make([]Session, 0, perfSessions)
	for i := 1; i <= perfSessions; i++ {
		sessions = append(sessions, Session{
			ID:       int64(i),
			Room:     "main-hall",
			Speakers: []string{fmt.Sprintf("spk-%d", i)},
			StartMs:  0,
			EndMs:    1_000_000_000_000,
		})
	}
	start := time.Now()
	c, err := NewChecker(sessions)
	if err != nil {
		t.Fatal(err)
	}
	it := c.Conflicts()
	for k := 0; k < 100; k++ {
		cf, ok := it.Next()
		if !ok {
			t.Fatalf("report ended early at %d", k)
		}
		wantA, wantB := int64(1), int64(k+2)
		if cf.A != wantA || cf.B != wantB {
			t.Fatalf("conflict %d = (%d,%d), want (%d,%d)", k, cf.A, cf.B, wantA, wantB)
		}
		if len(cf.Rooms) != 1 || cf.Rooms[0] != "main-hall" || len(cf.Speakers) != 0 {
			t.Fatalf("conflict %+v reasons wrong", cf)
		}
	}
	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Fatalf("first 100 dense conflicts took %v, want < 5s", elapsed)
	}
	if after := heapAllocMiB(); after > 256 {
		t.Fatalf("heap %.1f MiB exceeds 256 MiB", after)
	}
	t.Logf("dense: first 100 conflicts in %v", elapsed)
}
