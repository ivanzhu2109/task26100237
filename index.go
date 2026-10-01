package conflictcheck

import (
	"math"
	"sort"
)

// resourceIndex 是单个资源（房间或讲者）上的静态区间索引。
// 场次按开始时间升序排列，配合 maxEnd 线段树做重叠查询剪枝，
// 单次查询代价为 O(log n + k)，k 为命中的场次数。
type resourceIndex struct {
	sessions []*Session // 按 StartMs 升序（再按 EndMs、ID 决胜）
	maxEnd   []int64    // 线段树，节点 1 覆盖 [0, size)
	size     int        // 大于等于 len(sessions) 的最小 2 的幂
}

// newResourceIndex 构建索引；sessions 至少包含 2 个元素才有意义。
func newResourceIndex(sessions []*Session) *resourceIndex {
	sorted := make([]*Session, len(sessions))
	copy(sorted, sessions)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.StartMs != b.StartMs {
			return a.StartMs < b.StartMs
		}
		if a.EndMs != b.EndMs {
			return a.EndMs < b.EndMs
		}
		return a.ID < b.ID
	})

	size := 1
	for size < len(sorted) {
		size <<= 1
	}
	maxEnd := make([]int64, 2*size)
	for i := range maxEnd {
		maxEnd[i] = math.MinInt64
	}
	for i, s := range sorted {
		maxEnd[size+i] = s.EndMs
	}
	for i := size - 1; i >= 1; i-- {
		if maxEnd[2*i] > maxEnd[2*i+1] {
			maxEnd[i] = maxEnd[2*i]
		} else {
			maxEnd[i] = maxEnd[2*i+1]
		}
	}
	return &resourceIndex{sessions: sorted, maxEnd: maxEnd, size: size}
}

// query 访问所有与 [qs, qe) 存在正时长交集的场次，即满足
// StartMs < qe 且 EndMs > qs 的场次。访问顺序不保证有序。
func (r *resourceIndex) query(qs, qe int64, visit func(*Session)) {
	// 只需考虑 StartMs < qe 的前缀 [0, ub)。
	ub := sort.Search(len(r.sessions), func(i int) bool {
		return r.sessions[i].StartMs >= qe
	})
	if ub == 0 {
		return
	}
	r.collect(1, 0, r.size, ub, qs, visit)
}

// collect 在覆盖 [nl, nr) 的节点上递归收集与查询重叠的场次，
// 只下访 maxEnd > qs 的子树；qr 为候选上界（StartMs < qe 的前缀终点）。
func (r *resourceIndex) collect(node, nl, nr, qr int, qs int64, visit func(*Session)) {
	if nl >= qr || r.maxEnd[node] <= qs {
		return
	}
	if nr-nl == 1 {
		visit(r.sessions[nl])
		return
	}
	mid := nl + (nr-nl)/2
	r.collect(2*node, nl, mid, qr, qs, visit)
	r.collect(2*node+1, mid, nr, qr, qs, visit)
}
