package confcheck

import (
	"fmt"
	"sort"
)

// resIndex 是单个资源（某个房间或某位讲者）下场次的区间索引。
// entries 按 start 升序排列，prefMaxEnd[i] 为 entries[:i+1] 中 end 的最大值，
// 用于区间重叠查询时的提前终止。
type resIndex struct {
	starts     []int64
	ends       []int64
	sids       []int32 // 指向 Checker.sessions 的下标
	prefMaxEnd []int64
}

// Checker 保存一份排期的检查状态：按 ID 升序的场次及资源索引。
type Checker struct {
	sessions []Session            // 按 ID 升序
	rooms    map[string]*resIndex // 房间命名空间
	speakers map[string]*resIndex // 讲者命名空间（与房间互不相同）
}

// NewChecker 校验输入并构建检查状态。输入切片不会被修改或保留引用。
func NewChecker(sessions []Session) (*Checker, error) {
	if err := validate(sessions); err != nil {
		return nil, err
	}
	sorted := make([]Session, len(sessions))
	copy(sorted, sessions)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	c := &Checker{
		sessions: sorted,
		rooms:    make(map[string]*resIndex),
		speakers: make(map[string]*resIndex),
	}
	type ref struct {
		idx   *resIndex
		sid   int32
		start int64
		end   int64
	}
	// 先按资源聚合，再为每个资源构建有序索引。
	roomRefs := make(map[string][]ref)
	spkRefs := make(map[string][]ref)
	for i, s := range sorted {
		roomRefs[s.Room] = append(roomRefs[s.Room], ref{sid: int32(i), start: s.StartMs, end: s.EndMs})
		for _, sp := range s.Speakers {
			spkRefs[sp] = append(spkRefs[sp], ref{sid: int32(i), start: s.StartMs, end: s.EndMs})
		}
	}
	build := func(refs map[string][]ref) map[string]*resIndex {
		out := make(map[string]*resIndex, len(refs))
		for key, list := range refs {
			sort.Slice(list, func(i, j int) bool {
				if list[i].start != list[j].start {
					return list[i].start < list[j].start
				}
				return list[i].sid < list[j].sid
			})
			ri := &resIndex{
				starts:     make([]int64, len(list)),
				ends:       make([]int64, len(list)),
				sids:       make([]int32, len(list)),
				prefMaxEnd: make([]int64, len(list)),
			}
			var maxEnd int64 = -1 << 62
			for i, r := range list {
				ri.starts[i] = r.start
				ri.ends[i] = r.end
				ri.sids[i] = r.sid
				if r.end > maxEnd {
					maxEnd = r.end
				}
				ri.prefMaxEnd[i] = maxEnd
			}
			out[key] = ri
		}
		return out
	}
	c.rooms = build(roomRefs)
	c.speakers = build(spkRefs)
	return c, nil
}

func validate(sessions []Session) error {
	seen := make(map[int64]struct{}, len(sessions))
	for i := range sessions {
		s := &sessions[i]
		if s.ID <= 0 {
			return fmt.Errorf("session ID 必须为正整数: %d", s.ID)
		}
		if _, dup := seen[s.ID]; dup {
			return fmt.Errorf("session ID 重复: %d", s.ID)
		}
		seen[s.ID] = struct{}{}
		if len(s.Speakers) > 8 {
			return fmt.Errorf("session %d 讲者超过 8 名", s.ID)
		}
		if s.StartMs >= s.EndMs {
			return fmt.Errorf("session %d 开始时间必须严格早于结束时间", s.ID)
		}
		sp := make(map[string]struct{}, len(s.Speakers))
		for _, name := range s.Speakers {
			if _, dup := sp[name]; dup {
				return fmt.Errorf("session %d 讲者重复: %q", s.ID, name)
			}
			sp[name] = struct{}{}
		}
	}
	return nil
}
