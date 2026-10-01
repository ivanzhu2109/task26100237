// Package confcheck 提供会议排期冲突检查能力。
// 输入与检查状态全部保存在内存中，供上层发布流程直接调用。
package confcheck

// Session 表示一个待发布的场次。
type Session struct {
	ID       int64    // 唯一正整数场次 ID
	Room     string   // 房间标识
	Speakers []string // 讲者标识，最多 8 名且互不相同
	StartMs  int64    // 开始时间，UTC 毫秒
	EndMs    int64    // 结束时间，UTC 毫秒，必须严格大于 StartMs
}

// Conflict 表示一对场次的冲突及其全部原因。
type Conflict struct {
	A        int64    // 较小的场次 ID
	B        int64    // 较大的场次 ID
	Rooms    []string // 共享的房间（0 或 1 个）
	Speakers []string // 全部共享讲者，升序
}
