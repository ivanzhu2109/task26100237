package conflictcheck

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

const (
	perfSessions    = 100_000
	perfTimeLimit   = 5 * time.Second
	perfMemLimitMiB = 256
)

// buildSparseSchedule 构造 10 万场次的稀疏排期：
//   - 5 万场次共用同一房间但首尾相接（不冲突），压测区间索引剪枝；
//   - 其余场次房间与讲者均唯一；
//   - 另植入 500 对确定冲突（同房间 + 2 名共享讲者）。
//
// 冲突总数 500，不超过 1000。
func buildSparseSchedule() []Session {
	sessions := make([]Session, 0, perfSessions)
	id := int64(1)
	const hour = int64(3_600_000)
	for i := 0; i < 50_000; i++ {
		sessions = append(sessions, Session{
			ID:       id,
			Room:     "SharedRoom",
			Speakers: uniqueSpeakers(id),
			StartMs:  int64(i) * hour,
			EndMs:    int64(i+1) * hour,
		})
		id++
	}
	for i := 0; i < 49_000; i++ {
		sessions = append(sessions, Session{
			ID:       id,
			Room:     fmt.Sprintf("Room-%d", id),
			Speakers: uniqueSpeakers(id),
			StartMs:  int64(i) * hour,
			EndMs:    int64(i)*hour + 30*60*1000,
		})
		id++
	}
	for k := 0; k < 500; k++ {
		base := int64(k) * hour
		sessions = append(sessions, Session{
			ID:       id,
			Room:     fmt.Sprintf("ConflictRoom-%d", k),
			Speakers: []string{fmt.Sprintf("shared-a-%d", k), fmt.Sprintf("shared-b-%d", k)},
			StartMs:  base,
			EndMs:    base + hour,
		})
		id++
		sessions = append(sessions, Session{
			ID:       id,
			Room:     fmt.Sprintf("ConflictRoom-%d", k),
			Speakers: []string{fmt.Sprintf("shared-a-%d", k), fmt.Sprintf("shared-b-%d", k)},
			StartMs:  base + hour/2,
			EndMs:    base + hour,
		})
		id++
	}
	return sessions
}

// buildDenseSchedule 构造 10 万场次全部同房间同时间窗的高密度排期，
// 冲突对数约 5×10^9（数十亿）。
func buildDenseSchedule() []Session {
	sessions := make([]Session, 0, perfSessions)
	for i := 0; i < perfSessions; i++ {
		id := int64(i + 1)
		sessions = append(sessions, Session{
			ID:       id,
			Room:     "BigRoom",
			Speakers: uniqueSpeakers(id),
			StartMs:  0,
			EndMs:    3_600_000,
		})
	}
	return sessions
}

func uniqueSpeakers(id int64) []string {
	speakers := make([]string, MaxSpeakers)
	for j := range speakers {
		speakers[j] = fmt.Sprintf("spk-%d-%d", id, j)
	}
	return speakers
}

func heapAllocMiB() float64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapAlloc) / (1 << 20)
}

func TestPerfSparseFullReport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf test in -short mode")
	}
	runtime.GC()
	base := heapAllocMiB()
	sessions := buildSparseSchedule()
	if len(sessions) != perfSessions {
		t.Fatalf("got %d sessions, want %d", len(sessions), perfSessions)
	}

	start := time.Now()
	c := mustChecker(t, sessions)
	peak := heapAllocMiB()
	var all []Conflict
	for {
		batch, done := c.Next(1000)
		all = append(all, batch...)
		if heapAllocMiB() > peak {
			peak = heapAllocMiB()
		}
		if done {
			break
		}
	}
	elapsed := time.Since(start)
	runtime.KeepAlive(sessions)
	runtime.KeepAlive(c)
	peak -= base

	if len(all) != 500 {
		t.Fatalf("got %d conflicts, want 500", len(all))
	}
	for i := 1; i < len(all); i++ {
		a, b := all[i-1], all[i]
		if a.FirstID > b.FirstID || (a.FirstID == b.FirstID && a.SecondID >= b.SecondID) {
			t.Fatalf("report not sorted at %d: %+v then %+v", i, a, b)
		}
	}
	for _, cf := range all {
		if cf.Room == "" || len(cf.Speakers) != 2 {
			t.Fatalf("incomplete reasons: %+v", cf)
		}
	}
	if elapsed > perfTimeLimit {
		t.Fatalf("sparse full report took %v, limit %v", elapsed, perfTimeLimit)
	}
	if peak > perfMemLimitMiB {
		t.Fatalf("peak heap delta %.1f MiB exceeds %d MiB", peak, perfMemLimitMiB)
	}
	t.Logf("sparse: %d conflicts in %v, heap delta %.1f MiB", len(all), elapsed, peak)
}

func TestPerfDenseFirstBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf test in -short mode")
	}
	runtime.GC()
	base := heapAllocMiB()
	sessions := buildDenseSchedule()

	start := time.Now()
	c := mustChecker(t, sessions)
	batch, done := c.Next(100)
	elapsed := time.Since(start)
	peak := heapAllocMiB()
	runtime.KeepAlive(sessions)
	runtime.KeepAlive(c)
	peak -= base

	if done {
		t.Fatal("dense schedule exhausted after first batch, want billions of pairs")
	}
	if len(batch) != 100 {
		t.Fatalf("got %d conflicts, want 100", len(batch))
	}
	for i, cf := range batch {
		if cf.FirstID != 1 || cf.SecondID != int64(i+2) {
			t.Fatalf("batch[%d] = (%d,%d), want (1,%d)", i, cf.FirstID, cf.SecondID, i+2)
		}
		if cf.Room != "BigRoom" {
			t.Fatalf("missing shared room: %+v", cf)
		}
	}
	if elapsed > perfTimeLimit {
		t.Fatalf("dense first batch took %v, limit %v", elapsed, perfTimeLimit)
	}
	if peak > perfMemLimitMiB {
		t.Fatalf("peak heap delta %.1f MiB exceeds %d MiB", peak, perfMemLimitMiB)
	}
	t.Logf("dense: first 100 of ~5e9 conflicts in %v, heap delta %.1f MiB", elapsed, peak)
}

// TestPerfSparsePartialEqualsFull 验证 10 万场次下分段读取与完整读取一致。
func TestPerfSparsePartialEqualsFull(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf test in -short mode")
	}
	sessions := buildSparseSchedule()
	full := collectAll(t, mustChecker(t, sessions))
	for _, step := range []int{1, 7, 100} {
		got := collectBy(mustChecker(t, sessions), step)
		if len(got) != len(full) {
			t.Fatalf("step %d: got %d, want %d", step, len(got), len(full))
		}
		for i := range got {
			if !conflictsEqual(got[i], full[i]) {
				t.Fatalf("step %d item %d: got %+v, want %+v", step, i, got[i], full[i])
			}
		}
	}
}

func conflictsEqual(a, b Conflict) bool {
	if a.FirstID != b.FirstID || a.SecondID != b.SecondID || a.Room != b.Room {
		return false
	}
	if len(a.Speakers) != len(b.Speakers) {
		return false
	}
	for i := range a.Speakers {
		if a.Speakers[i] != b.Speakers[i] {
			return false
		}
	}
	return true
}
