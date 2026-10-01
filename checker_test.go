package confcheck

import (
	"fmt"
	"reflect"
	"testing"
)

func collectAll(t *testing.T, c *Checker) []Conflict {
	t.Helper()
	var out []Conflict
	it := c.Conflicts()
	for {
		cf, ok := it.Next()
		if !ok {
			return out
		}
		out = append(out, cf)
	}
}

// 跨赛道、同时共享房间与多名讲者：只产生一条冲突且原因齐全。
func TestCrossTrackRoomAndSpeakers(t *testing.T) {
	c, err := NewChecker([]Session{
		{ID: 1, Room: "R1", Speakers: []string{"alice", "bob", "carol"}, StartMs: 1000, EndMs: 2000},
		{ID: 2, Room: "R1", Speakers: []string{"carol", "alice", "dave"}, StartMs: 1500, EndMs: 3000},
		{ID: 3, Room: "R2", Speakers: []string{"erin"}, StartMs: 1000, EndMs: 9000},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := collectAll(t, c)
	want := []Conflict{{A: 1, B: 2, Rooms: []string{"R1"}, Speakers: []string{"alice", "carol"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// 区间规则：端点相接不冲突；完全包含、相同起止时间冲突。
func TestIntervalRules(t *testing.T) {
	c, err := NewChecker([]Session{
		{ID: 1, Room: "R", StartMs: 0, EndMs: 1000},    // 与 2 端点相接 -> 不冲突
		{ID: 2, Room: "R", StartMs: 1000, EndMs: 2000}, // 被 4 完全包含 -> 冲突
		{ID: 3, Room: "R", StartMs: 5000, EndMs: 6000}, // 与 5 相同起止 -> 冲突
		{ID: 4, Room: "R", StartMs: 500, EndMs: 9000},
		{ID: 5, Room: "R", StartMs: 5000, EndMs: 6000},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := collectAll(t, c)
	var pairs [][2]int64
	for _, cf := range got {
		pairs = append(pairs, [2]int64{cf.A, cf.B})
		if len(cf.Rooms) != 1 || cf.Rooms[0] != "R" {
			t.Fatalf("conflict %+v missing room reason", cf)
		}
	}
	want := [][2]int64{{1, 4}, {2, 4}, {3, 4}, {3, 5}, {4, 5}}
	if !reflect.DeepEqual(pairs, want) {
		t.Fatalf("got pairs %v, want %v", pairs, want)
	}
}

// 房间与讲者是不同资源类型：标识值相同不代表同一资源。
func TestRoomAndSpeakerNamespacesDistinct(t *testing.T) {
	c, err := NewChecker([]Session{
		{ID: 1, Room: "R1", Speakers: []string{"x"}, StartMs: 0, EndMs: 100},
		{ID: 2, Room: "x", Speakers: []string{"y"}, StartMs: 0, EndMs: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := collectAll(t, c); len(got) != 0 {
		t.Fatalf("expected no conflict, got %+v", got)
	}
}

// 报告按 (A, B) 升序，且与输入排列无关。
func TestOrderingAndInputPermutation(t *testing.T) {
	base := []Session{
		{ID: 10, Room: "R", Speakers: []string{"s1"}, StartMs: 0, EndMs: 100},
		{ID: 5, Room: "R", Speakers: []string{"s2"}, StartMs: 50, EndMs: 150},
		{ID: 7, Room: "R", Speakers: []string{"s1"}, StartMs: 120, EndMs: 200},
		{ID: 3, Room: "Q", Speakers: []string{"s2"}, StartMs: 0, EndMs: 300},
	}
	rev := make([]Session, len(base))
	for i, s := range base {
		rev[len(base)-1-i] = s
	}
	c1, _ := NewChecker(base)
	c2, _ := NewChecker(rev)
	r1 := collectAll(t, c1)
	r2 := collectAll(t, c2)
	if !reflect.DeepEqual(r1, r2) {
		t.Fatalf("input permutation changed report:\n%v\n%v", r1, r2)
	}
	for i := 1; i < len(r1); i++ {
		if r1[i-1].A > r1[i].A || (r1[i-1].A == r1[i].A && r1[i-1].B >= r1[i].B) {
			t.Fatalf("report not sorted at %d: %+v then %+v", i, r1[i-1], r1[i])
		}
	}
	// 冲突对: (3,5) 共享 s2; (3,10)? 房间不同、讲者不同 -> 否; (5,10) 同房间重叠;
	// (5,7) 同房间重叠; (7,10)? 10 结束于 100, 7 开始于 120 -> 否。
	var pairs [][2]int64
	for _, cf := range r1 {
		pairs = append(pairs, [2]int64{cf.A, cf.B})
	}
	want := [][2]int64{{3, 5}, {5, 7}, {5, 10}}
	if !reflect.DeepEqual(pairs, want) {
		t.Fatalf("got pairs %v, want %v", pairs, want)
	}
}

// 分段读取与一次读完内容一致。
func TestChunkedReadsMatchFullRead(t *testing.T) {
	var sessions []Session
	id := int64(1)
	for i := 0; i < 50; i++ {
		sessions = append(sessions, Session{
			ID: id, Room: fmt.Sprintf("R%d", i%3),
			Speakers: []string{fmt.Sprintf("s%d", i%5)},
			StartMs:  int64(i % 10), EndMs: int64(i%10) + 100,
		})
		id++
	}
	c, err := NewChecker(sessions)
	if err != nil {
		t.Fatal(err)
	}
	full := collectAll(t, c)
	if len(full) == 0 {
		t.Fatal("expected some conflicts")
	}
	// 以 7 条为一段分批读取。
	it := c.Conflicts()
	var chunked []Conflict
	buf := make([]Conflict, 0, 7)
	for {
		cf, ok := it.Next()
		if !ok {
			break
		}
		buf = append(buf, cf)
		if len(buf) == 7 {
			chunked = append(chunked, buf...)
			buf = buf[:0]
		}
	}
	chunked = append(chunked, buf...)
	if !reflect.DeepEqual(full, chunked) {
		t.Fatalf("chunked read differs from full read")
	}
}

// 输入校验：非法 ID、重复 ID、讲者过多、讲者重复、时间区间非法。
func TestValidation(t *testing.T) {
	ok := Session{ID: 1, Room: "R", StartMs: 0, EndMs: 1}
	cases := []Session{
		{ID: 0, Room: "R", StartMs: 0, EndMs: 1},
		{ID: 2, Room: "R", StartMs: 0, EndMs: 1, Speakers: []string{"a", "a"}},
		{ID: 2, Room: "R", StartMs: 5, EndMs: 5},
		{ID: 2, Room: "R", StartMs: 5, EndMs: 4},
		{ID: 2, Room: "R", StartMs: 0, EndMs: 1, Speakers: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}},
	}
	for _, bad := range cases {
		if _, err := NewChecker([]Session{ok, bad}); err == nil {
			t.Fatalf("expected error for %+v", bad)
		}
	}
	if _, err := NewChecker([]Session{ok, ok}); err == nil {
		t.Fatal("expected duplicate ID error")
	}
}

// 空排期与单场排期没有冲突。
func TestEmptyAndSingle(t *testing.T) {
	c, _ := NewChecker(nil)
	if got := collectAll(t, c); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
	c, _ = NewChecker([]Session{{ID: 1, Room: "R", StartMs: 0, EndMs: 1}})
	if got := collectAll(t, c); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

// 随机排期与 O(n^2) 暴力枚举对拍：内容、原因与顺序完全一致。
func TestBruteForceCrossCheck(t *testing.T) {
	rng := uint64(42)
	next := func(n int64) int64 {
		rng = rng*6364136223846793005 + 1442695040888963407
		return int64(rng>>33) % n
	}
	var sessions []Session
	for i := 0; i < 300; i++ {
		start := next(1000)
		speakers := make([]string, 0, 3)
		for j := 0; j < 3; j++ {
			speakers = append(speakers, string(rune('a'+rune(next(6))+rune(j*7))))
		}
		// 去重讲者
		seen := map[string]bool{}
		uniq := speakers[:0]
		for _, s := range speakers {
			if !seen[s] {
				seen[s] = true
				uniq = append(uniq, s)
			}
		}
		sessions = append(sessions, Session{
			ID:       int64(i + 1),
			Room:     string(rune('A' + rune(next(4)))),
			Speakers: uniq,
			StartMs:  start,
			EndMs:    start + 1 + next(200),
		})
	}
	c, err := NewChecker(sessions)
	if err != nil {
		t.Fatal(err)
	}
	got := collectAll(t, c)
	var want []Conflict
	for i := 0; i < len(sessions); i++ {
		for j := i + 1; j < len(sessions); j++ {
			a, b := sessions[i], sessions[j]
			if a.StartMs >= b.EndMs || b.StartMs >= a.EndMs {
				continue
			}
			cf := buildConflict(&a, &b)
			if len(cf.Rooms) == 0 && len(cf.Speakers) == 0 {
				continue
			}
			want = append(want, cf)
		}
	}
	sortConflicts(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mismatch:\ngot  %d conflicts, first diff search\nwant %d conflicts", len(got), len(want))
	}
}

func sortConflicts(cs []Conflict) {
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && (cs[j].A < cs[j-1].A || (cs[j].A == cs[j-1].A && cs[j].B < cs[j-1].B)); j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
}
