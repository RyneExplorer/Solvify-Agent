// Package eventch 提供「带 context 守卫」的通道发送，供 SSE 事件生产端统一使用。
//
// 背景（详见 docs/代码审查报告-执行流程与耦合.md P0-1）：
// 事件发送点原来全是裸写 `ch <- ev`。浏览器关闭 SSE 连接后 net/http 会取消请求 context，
// gin 的 c.Stream 随即不再从通道收事件；此时通道一旦写满（容量 100），生产端就永久阻塞，
// 整条 goroutine 链（含 eino Runner、DB 连接、上游 LLM 流）全部泄漏 ——
// 反复刷新页面即可把连接池打满。
package eventch

import "context"

// Send 向 ch 投递 v。若 ctx 已取消（客户端断连 / 请求超时），则丢弃该事件并立即返回，绝不阻塞。
//
// 为什么选「丢弃」而不是「返回失败让调用方中断」：
//
//   - 中断会改变控制流，而发送点后面往往还有必须执行的收尾逻辑。典型如
//     internal/agent/runner_adapter.go：发完事件后要删除 checkpoint 字节行
//     （见 fix(checkpoint) 那次提交，堵的是 agent_checkpoints 无限堆积）。
//     在发送点 return 会顺手跳过这些收尾，等于用新 bug 换旧 bug。
//   - 「丢弃」把写通道从「可能永久阻塞」降级为「尽力而为」，对上游是零行为变化：
//     断连时这个请求本来就是废的；而继续跑完并落库，用户刷新后还能看到回答。
//
// 所以调用方不需要根据任何返回值决定是否中断 —— 直接把裸写换成 Send 即可。
func Send[T any](ctx context.Context, ch chan<- T, v T) {
	select {
	case ch <- v:
	case <-ctx.Done():
	}
}
