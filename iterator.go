package confcheck

import "sort"

// Iterator 按 (A, B) 升序惰性地产出冲突。
// 仅在调用方读取时才计算后续冲突，因此即使完整报告有数十亿对，
// 前 100 条也能快速交付；多次分段读取与一次读完内容一致。
type Iterator struct {
	c       *Checker
	aIdx    int     // 当前作为 A 侧的场次下标（sessions 按 ID 升序）
	cands   []int32 // 当前 A 的候选 B 下标，按 ID 升序
	candPos int
	started bool
	visited []int32 // 代际标记去重数组，避免每轮清理
	gen     int32
}

// Conflicts 返回一个新的冲突迭代器。同一 Checker 可创建多个迭代器。
func (c *Checker) Conflicts() *Iterator {
	return &Iterator{c: c, visited: make([]int32, len(c.sessions))}
}

// Next 返回下一条冲突；ok 为 false 表示报告结束。
func (it *Iterator) Next() (Conflict, bool) {
	if !it.started {
		it.started = true
		if len(it.c.sessions) == 0 {
			return Conflict{}, false
		}
		it.aIdx = 0
		it.loadCandidates()
	}
	for {
		if it.candPos < len(it.cands) {
			b := it.c.sessions[it.cands[it.candPos]]
			it.candPos++
			a := it.c.sessions[it.aIdx]
			return buildConflict(&a, &b), true
		}
		it.aIdx++
		if it.aIdx >= len(it.c.sessions) {
			return Conflict{}, false
		}
		it.loadCandidates()
	}
}

// loadCandidates 收集与 sessions[aIdx] 冲突且 ID 更大的全部场次下标，按 ID 升序。
func (it *Iterator) loadCandidates() {
	it.cands = it.cands[:0]
	it.candPos = 0
	a := &it.c.sessions[it.aIdx]
	it.gen++
	gen := it.gen
	collect := func(ri *resIndex) {
		if ri == nil {
			return
		}
		// 所有与 [StartMs, EndMs) 正时长重叠的条目满足 start < EndMs；
		// 从右向左扫描，借助 prefMaxEnd 提前终止。
		lo := sort.Search(len(ri.starts), func(i int) bool { return ri.starts[i] >= a.EndMs })
		for i := lo - 1; i >= 0 && ri.prefMaxEnd[i] > a.StartMs; i-- {
			if ri.ends[i] <= a.StartMs {
				continue
			}
			sid := ri.sids[i]
			if sid <= int32(it.aIdx) || it.visited[sid] == gen {
				continue
			}
			it.visited[sid] = gen
			it.cands = append(it.cands, sid)
		}
	}
	collect(it.c.rooms[a.Room])
	for _, sp := range a.Speakers {
		collect(it.c.speakers[sp])
	}
	sort.Slice(it.cands, func(i, j int) bool { return it.cands[i] < it.cands[j] })
}

// buildConflict 汇总一对场次的全部冲突原因：共享房间及全部共享讲者。
func buildConflict(a, b *Session) Conflict {
	var rooms []string
	if a.Room == b.Room {
		rooms = []string{a.Room}
	}
	var speakers []string
	for _, sa := range a.Speakers {
		for _, sb := range b.Speakers {
			if sa == sb {
				speakers = append(speakers, sa)
				break
			}
		}
	}
	sort.Strings(speakers)
	return Conflict{A: a.ID, B: b.ID, Rooms: rooms, Speakers: speakers}
}
