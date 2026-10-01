// Package conflictcheck 提供会议排期冲突检查能力。
//
// 一次检查接收单个会议的一份完整待发布排期，所有输入与检查状态
// 均保存在内存中。冲突报告按（较小场次 ID，较大场次 ID）升序，
// 通过 Checker.Next 分段流式读取，分段读取与一次读完内容一致。
package conflictcheck

import (
	"fmt"
	"sort"
)

// MaxSpeakers 是单个场次允许的最大讲者数。
const MaxSpeakers = 8

// Session 表示一个待发布排期中的场次。
type Session struct {
	ID       int64    // 唯一正整数场次 ID
	Room     string   // 房间标识
	Speakers []string // 讲者标识，最多 8 名且互不重复
	StartMs  int64    // 开始时间，UTC 毫秒时间戳
	EndMs    int64    // 结束时间，UTC 毫秒时间戳，必须严格大于 StartMs
}

// Conflict 表示一对场次之间的冲突及其全部原因。
type Conflict struct {
	FirstID  int64    // 较小的场次 ID
	SecondID int64    // 较大的场次 ID
	Room     string   // 共享房间；不共享房间时为空字符串
	Speakers []string // 全部共享讲者，按字典序升序；无共享讲者时为 nil
}

// Checker 保存一次检查的状态，支持分段流式读取冲突报告。
// 不是并发安全的。
type Checker struct {
	sessions []*Session // 按 ID 升序
	rooms    map[string]*resourceIndex
	speakers map[string]*resourceIndex

	nextSession int        // 下一个作为冲突对前者待展开的场次下标
	pending     []Conflict // 当前前者尚未交付的冲突
	pendingPos  int
	exhausted   bool
}

// NewChecker 校验输入并构建检查状态。输入切片在调用后可由调用方自行处理，
// Checker 内部持有必要的副本与索引。
func NewChecker(sessions []Session) (*Checker, error) {
	if err := validate(sessions); err != nil {
		return nil, err
	}

	sorted := make([]*Session, len(sessions))
	roomUse := make(map[string]int)
	speakerUse := make(map[string]int)
	for i := range sessions {
		s := sessions[i]
		speakers := make([]string, len(s.Speakers))
		copy(speakers, s.Speakers)
		sorted[i] = &Session{
			ID:       s.ID,
			Room:     s.Room,
			Speakers: speakers,
			StartMs:  s.StartMs,
			EndMs:    s.EndMs,
		}
		roomUse[s.Room]++
		for _, sp := range s.Speakers {
			speakerUse[sp]++
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	c := &Checker{
		sessions: sorted,
		rooms:    make(map[string]*resourceIndex),
		speakers: make(map[string]*resourceIndex),
	}
	// 只被单个场次使用的资源不可能产生冲突，不为它们建索引。
	roomMembers := make(map[string][]*Session)
	for _, s := range sorted {
		if roomUse[s.Room] >= 2 {
			roomMembers[s.Room] = append(roomMembers[s.Room], s)
		}
	}
	for room, members := range roomMembers {
		c.rooms[room] = newResourceIndex(members)
	}
	speakerMembers := make(map[string][]*Session)
	for _, s := range sorted {
		for _, sp := range s.Speakers {
			if speakerUse[sp] >= 2 {
				speakerMembers[sp] = append(speakerMembers[sp], s)
			}
		}
	}
	for sp, members := range speakerMembers {
		c.speakers[sp] = newResourceIndex(members)
	}
	return c, nil
}

func validate(sessions []Session) error {
	seen := make(map[int64]struct{}, len(sessions))
	for i := range sessions {
		s := &sessions[i]
		if s.ID <= 0 {
			return fmt.Errorf("conflictcheck: 场次 ID 必须为正整数，第 %d 项 ID=%d", i, s.ID)
		}
		if _, dup := seen[s.ID]; dup {
			return fmt.Errorf("conflictcheck: 场次 ID %d 重复", s.ID)
		}
		seen[s.ID] = struct{}{}
		if s.StartMs >= s.EndMs {
			return fmt.Errorf("conflictcheck: 场次 %d 开始时间必须严格早于结束时间", s.ID)
		}
		if len(s.Speakers) > MaxSpeakers {
			return fmt.Errorf("conflictcheck: 场次 %d 讲者数 %d 超过上限 %d", s.ID, len(s.Speakers), MaxSpeakers)
		}
		sp := make(map[string]struct{}, len(s.Speakers))
		for _, name := range s.Speakers {
			if _, dup := sp[name]; dup {
				return fmt.Errorf("conflictcheck: 场次 %d 讲者 %q 重复", s.ID, name)
			}
			sp[name] = struct{}{}
		}
	}
	return nil
}

// Next 返回报告中至多 n 条后续冲突，按（FirstID, SecondID）升序。
// done 为 true 表示本次返回后报告已全部交付（batch 中可能仍含最后若干条），
// 此后继续调用将返回 (nil, true)。n <= 0 时不消耗任何内容。
func (c *Checker) Next(n int) (batch []Conflict, done bool) {
	if c.exhausted {
		return nil, true
	}
	if n <= 0 {
		return nil, false
	}
	batch = make([]Conflict, 0, n)
	for len(batch) < n {
		if c.pendingPos >= len(c.pending) {
			if !c.advance() {
				c.exhausted = true
				return batch, true
			}
			continue
		}
		batch = append(batch, c.pending[c.pendingPos])
		c.pendingPos++
	}
	return batch, false
}

// advance 展开下一个场次的全部冲突对作为缓冲；返回 false 表示没有更多场次。
func (c *Checker) advance() bool {
	for c.nextSession < len(c.sessions) {
		s := c.sessions[c.nextSession]
		c.nextSession++
		c.pending = c.pending[:0]
		c.pendingPos = 0
		c.collectPartners(s)
		if len(c.pending) > 0 {
			return true
		}
	}
	return false
}

// collectPartners 找出与 s 冲突且 ID 更大的全部场次，按 ID 升序写入 c.pending。
func (c *Checker) collectPartners(s *Session) {
	seen := make(map[int64]*Session)
	add := func(p *Session) {
		if p.ID > s.ID {
			seen[p.ID] = p
		}
	}
	if idx, ok := c.rooms[s.Room]; ok {
		idx.query(s.StartMs, s.EndMs, add)
	}
	for _, sp := range s.Speakers {
		if idx, ok := c.speakers[sp]; ok {
			idx.query(s.StartMs, s.EndMs, add)
		}
	}
	if len(seen) == 0 {
		return
	}
	partners := make([]*Session, 0, len(seen))
	for _, p := range seen {
		partners = append(partners, p)
	}
	sort.Slice(partners, func(i, j int) bool { return partners[i].ID < partners[j].ID })
	for _, p := range partners {
		c.pending = append(c.pending, buildConflict(s, p))
	}
}

// buildConflict 构造 s（ID 较小）与 p 之间的冲突条目，列齐全部共享原因。
func buildConflict(s, p *Session) Conflict {
	c := Conflict{FirstID: s.ID, SecondID: p.ID}
	if s.Room == p.Room {
		c.Room = s.Room
	}
	inP := make(map[string]struct{}, len(p.Speakers))
	for _, sp := range p.Speakers {
		inP[sp] = struct{}{}
	}
	for _, sp := range s.Speakers {
		if _, ok := inP[sp]; ok {
			c.Speakers = append(c.Speakers, sp)
		}
	}
	sort.Strings(c.Speakers)
	return c
}
