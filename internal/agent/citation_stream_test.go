package agent

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestCitationStreamingMatchesWholeAnswer(t *testing.T) {
	r := &citationRegistry{}
	source := r.toolSources("knowledge_search", citationKBResult)[0]
	r.add(citationSource{Kind: "web", Title: "带括号的网页", URL: "https://example.com/a(b)"})
	for _, input := range []string{
		"直接结论：select。<cite id=\"" + source.ID + "\" />尾段。",
		"`<cite id=\"unknown\" />`\n```go\n<web url=\"x\" title=\"y\" />\n```\n<cite id=\"" + source.ID + "\" />",
		"~~~html\n<kb doc=\"示例\" chunk_id=\"none\" />\n~~~\n正文",
		"代码``包含`和<cite id=\"示例\" />``正文",
		"[带[嵌套]标题](https://example.com/a(b))结尾",
		"[未知](https://unseen.example.com)\n[目录](#section)\n普通[文字]。",
		"<WEB\nurl='https://unseen.example.com' title='x > y'/>结束",
		"未闭合<cite id=\"unknown\"",
		"文字[未闭合",
		"普通小于号<",
		"标签[<web url=\"https://unseen.example.com\" title=\"伪造\" />]",
		"转义\\` <cite id=\"" + source.ID + "\" />",
		"未闭合`<web url=\"https://unseen.example.com\" title=\"伪造\" />",
		"普通文字~~~<web url=\"https://unseen.example.com\" title=\"伪造\" />",
		"```inline```<web url=\"https://unseen.example.com\" title=\"伪造\" />",
		"```bad`info <web url=\"https://unseen.example.com\" title=\"伪造\" />",
	} {
		want := normalizeCitations(r, input)
		for step := 1; step < 19; step++ {
			stream := citationStream{registry: r}
			var got strings.Builder
			for i := 0; i < len(input); i += step {
				got.WriteString(stream.push(input[i:min(i+step, len(input))], false))
			}
			got.WriteString(stream.push("", true))
			if got.String() != want {
				t.Fatalf("分片%d改变正文：%q\ngot %q\nwant %q", step, input, got.String(), want)
			}
		}
	}
	code := "`<cite id=\"unknown\" />`\n```html\n<web url=\"x\" title=\"y\" />\n```"
	if got := normalizeCitations(r, code); got != code {
		t.Fatal("引用代码示例被改写", got)
	}
	if got := normalizeCitations(r, `[<web url="https://unseen.example.com" title="伪造" />](https://unseen.example.com)`); strings.Contains(got, "<web") {
		t.Fatal("Markdown标签绕过了引用校验", got)
	}
	for _, prefix := range []string{"未闭合`", "普通文字~~~", "```inline```", "```bad`info "} {
		if got := normalizeCitations(r, prefix+`<web url="https://unseen.example.com" title="伪造" />`); strings.Contains(got, "<web") {
			t.Fatalf("非代码正文绕过校验: %q", got)
		}
	}
	if got := normalizeCitations(r, `[<web url="https://unseen.example.com" title="伪造" />](#section)`); strings.Contains(got, "<web") {
		t.Fatal("页内链接标签绕过校验", got)
	}
}

func TestCitationEventsStreamBeforeTerminalAndPreserveMetadata(t *testing.T) {
	for _, terminal := range []string{EventDone, EventError, EventInterrupt} {
		t.Run(terminal, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			out := make(chan Event, 8)
			in, finished := citationEventChannel(ctx, &citationRegistry{}, out)
			in <- Event{Type: EventAnswer, Content: "首段正文", PlanID: "plan", TaskID: "answer"}
			select {
			case first := <-out:
				if first.Content != "首段正文" {
					t.Fatalf("首段未及时输出: %+v", first)
				}
			case <-ctx.Done():
				t.Fatal("普通正文等待了尾段")
			}
			in <- Event{Type: EventAnswer, Content: `<cite id="missing`, PlanID: "plan", TaskID: "answer"}
			in <- Event{Type: terminal, Content: `首段正文<cite id="missing`, Done: true}
			close(in)
			select {
			case <-finished:
			case <-ctx.Done():
				t.Fatal("引用管道未退出")
			}
			flush, end := <-out, <-out
			if flush.Type != EventAnswer || flush.Content != "（来源未核验）" || flush.PlanID != "plan" || flush.TaskID != "answer" {
				t.Fatalf("尾部或归属丢失: %+v", flush)
			}
			if end.Type != terminal || terminal == EventDone && end.Content != "首段正文（来源未核验）" {
				t.Fatalf("终态正文不一致: %+v", end)
			}
			if len(out) != 0 {
				t.Fatal("终态之后追加了正文")
			}
		})
	}
}

func TestCitationEventsCancellationDrainsProducer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan Event)
	in, finished := citationEventChannel(ctx, &citationRegistry{}, out)
	in <- Event{Type: EventAnswer, Content: "消费者不读取"}
	cancel()
	for i := 0; i < 300; i++ {
		in <- Event{Type: EventAnswer, Content: "取消后的正文"}
	}
	close(in)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("取消后留下阻塞的引用goroutine")
	}
}

func TestCitationStreamingKeepsUTF8CodeChunks(t *testing.T) {
	stream := citationStream{}
	for _, part := range []string{"```go\n", "中文代码", "😀select", "\n```", "\n尾段"} {
		if got := stream.push(part, false); !utf8.ValidString(got) {
			t.Fatalf("保留围栏尾部切坏了Unicode: %q", got)
		}
	}
	if got := stream.push("", true); !utf8.ValidString(got) {
		t.Fatalf("收尾切坏了Unicode: %q", got)
	}
}
