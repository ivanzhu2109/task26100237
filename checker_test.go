package conflictcheck

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// collectAll 一次性读完整份报告。
func collectAll(t *testing.T, c *Checker) []Conflict {
	t.Helper()
	var all []Conflict
	for {
		batch, done := c.Next(1024)
		all = append(all, batch...)
		if done {
			return all
		}
	}
}

// collectBy 以固定步长分段读完整份报告。
func collectBy(c *Checker, step int) []Conflict {
	var all []Conflict
	for {
		batch, done := c.Next(step)
		all = append(all, batch...)
		if done {
			return all
		}
	}
}

func mustChecker(t *testing.T, sessions []Session) *Checker {
	t.Helper()
	c, err := NewChecker(sessions)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	return c
}

func TestCrossTrackSharedRoomAndSpeakers(t *testing.T) {
	// 两个赛道不同的场次同时共享房间与多名讲者：只产生一条冲突，且原因齐全。
	c := mustChecker(t, []Session{
		{ID: 1, Room: "R1", Speakers: []string{"alice", "bob", "carol"}, StartMs: 1000, EndMs: 2000},
		{ID: 2, Room: "R1", Speakers: []string{"carol", "bob", "dave"}, StartMs: 1500, EndMs: 2500},
	})
	got := collectAll(t, c)
	want := []Conflict{{
		FirstID:  1,
		SecondID: 2,
		Room:     "R1",
		Speakers: []string{"bob", "carol"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestIntervalRules(t *testing.T) {
	const room = "R"
	cases := []struct {
		name         string
		a, b         Session
		wantConflict bool
	}{
		{
			name: "端点相接不构成冲突",
			a:    Session{ID: 1, Room: room, StartMs: 1000, EndMs: 2000},
			b:    Session{ID: 2, Room: room, StartMs: 2000, EndMs: 3000},
		},
		{
			name: "端点相接（反向）不构成冲突",
			a:    Session{ID: 1, Room: room, StartMs: 2000, EndMs: 3000},
			b:    Session{ID: 2, Room: room, StartMs: 1000, EndMs: 2000},
		},
		{
			name:         "完全包含构成冲突",
			a:            Session{ID: 1, Room: room, StartMs: 1000, EndMs: 9000},
			b:            Session{ID: 2, Room: room, StartMs: 3000, EndMs: 4000},
			wantConflict: true,
		},
		{
			name:         "相同起止时间构成冲突",
			a:            Session{ID: 1, Room: room, StartMs: 1000, EndMs: 2000},
			b:            Session{ID: 2, Room: room, StartMs: 1000, EndMs: 2000},
			wantConflict: true,
		},
		{
			name:         "跨日部分重叠构成冲突",
			a:            Session{ID: 1, Room: room, StartMs: 86_400_000 - 1000, EndMs: 86_400_000 + 1000},
			b:            Session{ID: 2, Room: room, StartMs: 86_400_000, EndMs: 86_400_000 + 2000},
			wantConflict: true,
		},
		{
			name:         "仅重叠一毫秒也构成冲突",
			a:            Session{ID: 1, Room: room, StartMs: 1000, EndMs: 2001},
			b:            Session{ID: 2, Room: room, StartMs: 2000, EndMs: 3000},
			wantConflict: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := collectAll(t, mustChecker(t, []Session{tc.a, tc.b}))
			if tc.wantConflict {
				if len(got) != 1 || got[0].FirstID != 1 || got[0].SecondID != 2 || got[0].Room != room {
					t.Fatalf("got %+v, want single room conflict (1,2)", got)
				}
			} else if len(got) != 0 {
				t.Fatalf("got %+v, want no conflict", got)
			}
		})
	}
}

func TestRoomAndSpeakerAreDistinctResourceTypes(t *testing.T) {
	// 房间 "X" 与讲者 "X" 标识值相同，但不是同一资源。
	c := mustChecker(t, []Session{
		{ID: 1, Room: "X", StartMs: 1000, EndMs: 2000},
		{ID: 2, Speakers: []string{"X"}, StartMs: 1000, EndMs: 2000},
	})
	if got := collectAll(t, c); len(got) != 0 {
		t.Fatalf("got %+v, want no conflict", got)
	}
}

func TestSpeakerOnlyConflictHasNoRoom(t *testing.T) {
	c := mustChecker(t, []Session{
		{ID: 3, Room: "A", Speakers: []string{"sam"}, StartMs: 0, EndMs: 100},
		{ID: 7, Room: "B", Speakers: []string{"sam"}, StartMs: 50, EndMs: 150},
	})
	got := collectAll(t, c)
	want := []Conflict{{FirstID: 3, SecondID: 7, Speakers: []string{"sam"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestNoTimeOverlapNoConflict(t *testing.T) {
	c := mustChecker(t, []Session{
		{ID: 1, Room: "R", Speakers: []string{"a"}, StartMs: 0, EndMs: 100},
		{ID: 2, Room: "R", Speakers: []string{"a"}, StartMs: 200, EndMs: 300},
	})
	if got := collectAll(t, c); len(got) != 0 {
		t.Fatalf("got %+v, want no conflict", got)
	}
}

func TestReportOrderingAndInputPermutation(t *testing.T) {
	base := []Session{
		{ID: 10, Room: "R1", Speakers: []string{"a"}, StartMs: 0, EndMs: 1000},
		{ID: 4, Room: "R1", Speakers: []string{"b"}, StartMs: 500, EndMs: 1500},
		{ID: 7, Room: "R2", Speakers: []string{"a"}, StartMs: 800, EndMs: 2000},
		{ID: 12, Room: "R3", Speakers: []string{"b", "a"}, StartMs: 900, EndMs: 3000},
		{ID: 30, Room: "R9", Speakers: []string{"z"}, StartMs: 0, EndMs: 1},
	}
	want := collectAll(t, mustChecker(t, base))
	// 期望顺序: (4,10) (4,12) (7,10) (7,12) (10,12)
	var wantPairs [][2]int64
	for _, c := range want {
		wantPairs = append(wantPairs, [2]int64{c.FirstID, c.SecondID})
	}
	expectPairs := [][2]int64{{4, 10}, {4, 12}, {7, 10}, {7, 12}, {10, 12}}
	if !reflect.DeepEqual(wantPairs, expectPairs) {
		t.Fatalf("pairs got %v, want %v", wantPairs, expectPairs)
	}

	// 输入排列变化不得改变报告内容。
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		perm := append([]Session(nil), base...)
		rng.Shuffle(len(perm), func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })
		got := collectAll(t, mustChecker(t, perm))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: got %+v, want %+v", trial, got, want)
		}
	}
}

func TestPartialReadsMatchFullRead(t *testing.T) {
	var sessions []Session
	id := int64(1)
	for i := 0; i < 50; i++ {
		sessions = append(sessions, Session{
			ID: id, Room: fmt.Sprintf("R%d", i%5),
			Speakers: []string{fmt.Sprintf("s%d", i%7)},
			StartMs:  int64(i % 10 * 100), EndMs: int64(i%10*100) + 500,
		})
		id++
	}
	full := collectAll(t, mustChecker(t, sessions))
	if len(full) == 0 {
		t.Fatal("expected some conflicts")
	}
	for _, step := range []int{1, 2, 3, 7, 100, 4096} {
		got := collectBy(mustChecker(t, sessions), step)
		if !reflect.DeepEqual(got, full) {
			t.Fatalf("step %d: partial read mismatch (%d vs %d items)", step, len(got), len(full))
		}
	}
	// 读完后继续读应返回 (nil, true)。
	c := mustChecker(t, sessions)
	collectAll(t, c)
	if batch, done := c.Next(10); batch != nil || !done {
		t.Fatalf("after exhaustion got (%v, %v), want (nil, true)", batch, done)
	}
}

func TestValidation(t *testing.T) {
	valid := Session{ID: 1, Room: "R", StartMs: 0, EndMs: 1}
	cases := []struct {
		name     string
		sessions []Session
	}{
		{"ID 非正", []Session{{ID: 0, Room: "R", StartMs: 0, EndMs: 1}}},
		{"ID 重复", []Session{valid, {ID: 1, Room: "R2", StartMs: 0, EndMs: 1}}},
		{"开始等于结束", []Session{{ID: 1, Room: "R", StartMs: 5, EndMs: 5}}},
		{"开始晚于结束", []Session{{ID: 1, Room: "R", StartMs: 6, EndMs: 5}}},
		{"讲者超过 8 名", []Session{{ID: 1, Room: "R", Speakers: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}, StartMs: 0, EndMs: 1}}},
		{"讲者重复", []Session{{ID: 1, Room: "R", Speakers: []string{"a", "a"}, StartMs: 0, EndMs: 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewChecker(tc.sessions); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
	if _, err := NewChecker([]Session{valid}); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	if _, err := NewChecker(nil); err != nil {
		t.Fatalf("empty input rejected: %v", err)
	}
}

func TestPairReportedOnceWithAllReasons(t *testing.T) {
	// 同一对共享房间和多名讲者，即使通过多个索引发现也只出现一次。
	c := mustChecker(t, []Session{
		{ID: 1, Room: "R", Speakers: []string{"x", "y", "z"}, StartMs: 0, EndMs: 100},
		{ID: 2, Room: "R", Speakers: []string{"z", "y", "x"}, StartMs: 0, EndMs: 100},
	})
	got := collectAll(t, c)
	if len(got) != 1 {
		t.Fatalf("got %d conflicts, want 1: %+v", len(got), got)
	}
	if got[0].Room != "R" || !reflect.DeepEqual(got[0].Speakers, []string{"x", "y", "z"}) {
		t.Fatalf("incomplete reasons: %+v", got[0])
	}
}
