package service

import (
	"reflect"
	"strings"
	"testing"

	dto "solvify-agent/internal/model/dto/response"
)

// TestDurationFieldsAreNeverOmitted 守住一条机械的字段约定：
// json 名以 _ms 结尾的字段，一律不许带 omitempty。
//
// 为什么这不是代码风格问题：0 是**合法的测量值** —— 亚毫秒的 span（QueryRewrite、
// eino.lambda 这类）用整数毫秒表达就是 0；而「字段不存在」的含义完全不同，是
// 「没采集到 / 这条记录没有该阶段」。挂上 omitempty 之后，两种语义在 JSON 里合并成
// 一个 undefined，前端只能猜（前端类型里 duration_ms?: number 就是这么来的）。
//
// 判据是机械的，所以守卫也写成机械的：新增耗时字段只要落在下面这几个
// 「会直接出给前端 / 出到日志」的结构体里，本测试自动覆盖，不需要有人记得去改它。
// ⚠️ 本测试曾经守 observability.Span / TraceResponse / entity.ChatTrace 等类型；
// 那些类型已随自研可观测性模块整体移除，故类型清单改成现存的两个。
//
// 回归价值：给任意一个字段补回 ,omitempty，本测试立刻变红。
func TestDurationFieldsAreNeverOmitted(t *testing.T) {
	types := []any{
		dto.TestResult{},      // 模型连通性测试结果（response_time_ms）
		syncTaskOverviewLog{}, // 同步任务概览日志（cost_ms）
	}
	for _, v := range types {
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			tag := f.Tag.Get("json")
			name := strings.Split(tag, ",")[0]
			if !strings.HasSuffix(name, "_ms") {
				continue
			}
			if strings.Contains(tag, "omitempty") {
				t.Errorf("%s.%s 的 json 名是 %q，不许带 omitempty："+
					"0 是合法测量值，字段缺失才是「没采集到」，两者必须能区分",
					rt.Name(), f.Name, name)
			}
		}
	}
}
