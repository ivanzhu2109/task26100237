// perfcheck 是可运行的性能用例：构造 10 万场次的稀疏与高密度排期，
// 验证首批交付耗时、完整报告耗时与内存占用。
//
// 运行：go run ./cmd/perfcheck
package main

import (
	"fmt"
	"runtime"
	"time"

	"conflictcheck"
)

const (
	totalSessions = 100_000
	timeLimit     = 5 * time.Second
	memLimitMiB   = 256
)

func main() {
	ok := true
	ok = runSparse() && ok
	ok = runDense() && ok
	if !ok {
		fmt.Println("RESULT: FAIL")
	} else {
		fmt.Println("RESULT: PASS")
	}
}

func runSparse() bool {
	fmt.Println("== 稀疏场景：10 万场次，500 对冲突，完整读取 ==")
	runtime.GC()
	base := heapAllocMiB()
	sessions := buildSparse()
	start := time.Now()
	c, err := conflictcheck.NewChecker(sessions)
	if err != nil {
		fmt.Println("NewChecker error:", err)
		return false
	}
	peak := heapAllocMiB()
	total := 0
	var first, last conflictcheck.Conflict
	sorted := true
	for {
		batch, done := c.Next(1000)
		for _, cf := range batch {
			if total > 0 && (last.FirstID > cf.FirstID ||
				(last.FirstID == cf.FirstID && last.SecondID >= cf.SecondID)) {
				sorted = false
			}
			if total == 0 {
				first = cf
			}
			last = cf
			total++
		}
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
	mem := peak - base
	pass := total == 500 && sorted && elapsed < timeLimit && mem < memLimitMiB
	fmt.Printf("冲突总数=%d 有序=%v 首条=(%d,%d) 耗时=%v(限%v) 峰值堆内存=%.1fMiB(限%dMiB) => %s\n",
		total, sorted, first.FirstID, first.SecondID, elapsed, timeLimit, mem, memLimitMiB, verdict(pass))
	return pass
}

func runDense() bool {
	fmt.Println("== 高密度场景：10 万场次同房间同时间窗，约 5e9 对冲突，读取首批 100 条 ==")
	runtime.GC()
	base := heapAllocMiB()
	sessions := buildDense()
	start := time.Now()
	c, err := conflictcheck.NewChecker(sessions)
	if err != nil {
		fmt.Println("NewChecker error:", err)
		return false
	}
	batch, done := c.Next(100)
	elapsed := time.Since(start)
	peak := heapAllocMiB()
	runtime.KeepAlive(sessions)
	runtime.KeepAlive(c)
	mem := peak - base
	ok := len(batch) == 100 && !done
	for i, cf := range batch {
		if cf.FirstID != 1 || cf.SecondID != int64(i+2) || cf.Room != "BigRoom" {
			ok = false
		}
	}
	pass := ok && elapsed < timeLimit && mem < memLimitMiB
	fmt.Printf("首批=%d条 内容正确=%v 耗时=%v(限%v) 峰值堆内存=%.1fMiB(限%dMiB) => %s\n",
		len(batch), ok, elapsed, timeLimit, mem, memLimitMiB, verdict(pass))
	return pass
}

func buildSparse() []conflictcheck.Session {
	sessions := make([]conflictcheck.Session, 0, totalSessions)
	id := int64(1)
	const hour = int64(3_600_000)
	add := func(room string, speakers []string, start, end int64) {
		sessions = append(sessions, conflictcheck.Session{
			ID: id, Room: room, Speakers: speakers, StartMs: start, EndMs: end,
		})
		id++
	}
	for i := 0; i < 50_000; i++ {
		add("SharedRoom", uniqueSpeakers(id), int64(i)*hour, int64(i+1)*hour)
	}
	for i := 0; i < 49_000; i++ {
		add(fmt.Sprintf("Room-%d", id), uniqueSpeakers(id), int64(i)*hour, int64(i)*hour+30*60*1000)
	}
	for k := 0; k < 500; k++ {
		base := int64(k) * hour
		room := fmt.Sprintf("ConflictRoom-%d", k)
		sp := []string{fmt.Sprintf("shared-a-%d", k), fmt.Sprintf("shared-b-%d", k)}
		add(room, sp, base, base+hour)
		add(room, sp, base+hour/2, base+hour)
	}
	return sessions
}

func buildDense() []conflictcheck.Session {
	sessions := make([]conflictcheck.Session, 0, totalSessions)
	for i := 0; i < totalSessions; i++ {
		id := int64(i + 1)
		sessions = append(sessions, conflictcheck.Session{
			ID: id, Room: "BigRoom", Speakers: uniqueSpeakers(id), StartMs: 0, EndMs: 3_600_000,
		})
	}
	return sessions
}

func uniqueSpeakers(id int64) []string {
	speakers := make([]string, conflictcheck.MaxSpeakers)
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

func verdict(pass bool) string {
	if pass {
		return "PASS"
	}
	return "FAIL"
}
