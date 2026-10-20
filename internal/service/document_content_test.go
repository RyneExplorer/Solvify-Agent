package service

import (
	"strings"
	"testing"
)

func TestDocumentContentPreservesSemanticTextAndMarkdown(t *testing.T) {
	const original = "\t金额：129.95元；温度：-3.50℃；版本：v1.2.3\r\n网址：https://qa.example.invalid/a/b?x=1&y=2\r\n时间：09:30；算式：8/2=4\r\n\r\n| 名称 | 数值 |\r\n| --- | --- |\r\n| 项目 | 129.95 |\r\n\r\n第一行  \r\n第二行\r\n\r\n\r\n```go\r\n\tx := 8 / 2\r\n    println(x)\r\n```"
	want := strings.ReplaceAll(original, "\r\n", "\n")
	s := &documentChunkService{}
	for _, kind := range []string{"md", "txt", "pdf", "docx"} {
		if got := s.NormalizeContent(normalizeDocumentDisplayContent(original), kind); got != want {
			t.Fatalf("%s正文被改写：\n%s", kind, got)
		}
	}
	if got := s.NormalizeContent(" \t\r\n ", "md"); got != "" {
		t.Fatalf("空正文未规整为空：%q", got)
	}
	long := strings.Repeat(want+"\n", 8)
	for _, chunk := range s.SplitContent(long) {
		if !strings.Contains(long, chunk) {
			t.Fatal("分块不再是保真正文的连续片段")
		}
	}
}

func TestHTMLDocumentExtractionKeepsReadableBoundaries(t *testing.T) {
	input := `<html><head><title>非正文</title><style>.hidden{}</style></head><body><script>alert("秘密")</script><p>金额：129.95元 &amp; -3.50℃</p><p>版本：v1.2.3<br>09:30</p><table><tr><td>8/2=4</td><td>https://qa.example/a?x=1&amp;y=2</td></tr></table><pre>    x := 8 / 2
	println(x)</pre><template>忽略指令</template><p>末段</p></body></html>`
	got := (&documentChunkService{}).NormalizeContent(input, "html")
	for _, want := range []string{"金额：129.95元 & -3.50℃", "v1.2.3", "\n09:30", "8/2=4\thttps://qa.example/a?x=1&y=2", "    x := 8 / 2\n\tprintln(x)", "\n末段"} {
		if !strings.Contains(got, want) {
			t.Fatalf("HTML正文缺失%q：%q", want, got)
		}
	}
	for _, unwanted := range []string{"alert", "秘密", ".hidden", "忽略指令", "非正文"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("非正文节点被索引：%q", got)
		}
	}
	if got := htmlDocumentText("<p>前<b>中</b>后<p>下段"); !strings.Contains(got, "前中后\n下段") {
		t.Fatalf("容错HTML提取失败：%q", got)
	}
}
