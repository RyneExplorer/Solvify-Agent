package tool

// ProgressReport 描述工具执行过程中的用户友好进度信息。
// 工具通过 ProgressReporter 接口生成此结构，Agent 层通用转发，不暴露内部调用细节。
type ProgressReport struct {
	Title  string // 简短标题，如 "正在检索知识库"
	Detail string // 补充信息，如查询内容、结果摘要
	Status string // 状态：running / success / warning / error
}

// ProgressReporter 工具可选实现此接口，提供用户友好的进度文案。
// 未实现此接口的工具，Agent 层不发送进度事件。
type ProgressReporter interface {
	// StartReport 返回工具开始执行时的进度报告，args 为 JSON 格式参数
	StartReport(args string) ProgressReport
	// ResultReport 返回工具执行完成后的进度报告，result 为执行结果，execErr 为执行错误
	ResultReport(result string, execErr error) ProgressReport
}
