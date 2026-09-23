package service

import (
	"slices"
	"strings"
	"testing"

	"solvify-agent/pkg/textseg"
)

// 回归背景（2026-09-23，第 2 步「2a」）：
// 建库侧关键词曾经是「英文正则 + 中文 2~12 字 ngram 穷举 → 按词频排序 → 砍到 20 个」，
// 与检索侧（gse 真分词）口径完全不同。后果是两侧求交集几乎撞不上 ——
// 正文里明明写着「布隆过滤器」，用户搜「布隆」也永远召回不到。
// 现在两侧共用 pkg/textseg，下面几条断言把「口径同源 + 不再截断 + 不落垃圾词」钉住。
//
// ⚠️ 期望值不是拍脑袋来的：`go run test1/rag_eval/probe_seg_sample.go` 打印过
// textseg.Extract 对这些输入的**真实**输出，断言按实测值写。
func TestDocumentChunkExtractKeywords_DelegatesToSharedTokenizer(t *testing.T) {
	s := &documentChunkService{}
	const content = "布隆过滤器有假阳性但无假阴性，可以用它挡住缓存穿透。"

	got := s.extractKeywords(content)
	want := textseg.Extract(content)

	// 1. 口径同源：必须与检索侧逐项、同序一致。
	//    这是「建库侧与检索侧不再各写一套切词规则」的直接证据。
	if !slices.Equal([]string(got), want) {
		t.Fatalf("关键词口径与 textseg.Extract 不一致：\n  建库侧 %v\n  检索侧 %v", got, want)
	}

	// 2. 专名必须真的入库（旧实现会把它切成 ngram 碎片，或按词频截断挤掉）
	for _, must := range []string{"布隆", "过滤器"} {
		if !slices.Contains(got, must) {
			t.Errorf("专名 %q 没进关键词：%v ⇒ 关键字路永远召回不到它", must, got)
		}
	}

	// 3. 每个词项都要含字母或数字（纯标点/空白对检索无意义）
	for _, k := range got {
		if !textseg.HasWordChar(k) {
			t.Errorf("纯标点词项 %q 不该入库：%v", k, got)
		}
	}
}

// 2a 的另一半：**去掉 20 个词的截断**。
// 截断是不可逆的 —— 词没落库，之后怎么调打分权重都补不回来，所以存储层不再挑词。
func TestDocumentChunkExtractKeywords_DoesNotTruncateToTwenty(t *testing.T) {
	s := &documentChunkService{}
	// 40 个互不相同的拉丁词项：gse 会把每个拉丁词整段保留
	// （实测 `kubectl get pods -n default` 切出的是整词而非字母碎片），
	// 于是词项数必然远超旧的 20 上限。
	content := strings.Join([]string{
		"kubectl", "scheduler", "controller", "operator", "ingress",
		"webhook", "sidecar", "daemonset", "statefulset", "configmap",
		"secret", "serviceaccount", "namespace", "endpoint", "kubelet",
		"apiserver", "etcd", "calico", "cilium", "flannel",
		"prometheus", "grafana", "loki", "tempo", "jaeger",
		"opentelemetry", "collector", "exporter", "sampler", "quota",
		"cronjob", "affinity", "toleration", "storageclass", "headless",
		"canary", "rollout", "mutate", "admission", "finalizer",
	}, " ")

	got := s.extractKeywords(content)

	if len(got) <= 20 {
		t.Fatalf("关键词被截断了：只有 %d 个词项（旧实现的上限是 20）⇒ 存储层不该再挑词：%v",
			len(got), got)
	}
	// 与 textseg 仍然同源（若不同源，长度差异就不只是截断引起的）
	if want := textseg.Extract(content); !slices.Equal([]string(got), want) {
		t.Fatalf("口径不一致：建库侧 %d 项，检索侧 %d 项", len(got), len(want))
	}
}

// 旧实现的 englishKeywordPattern（`[A-Za-z0-9_./:-]{2,64}`）**不要求含字母或数字**，
// 于是 markdown 分隔线 `---`、命令行短选项 `-n` 都会被当成「英文关键词」入库。
// 这类词项永远匹配不到有意义的 query，只会抬高关键字命中的分母、压低全部候选分。
func TestDocumentChunkExtractKeywords_DropsPunctuationOnlyTokens(t *testing.T) {
	s := &documentChunkService{}
	const content = "用法：kubectl get pods -n default\n\n---\n\n如上。"

	got := s.extractKeywords(content)

	for _, junk := range []string{"---", "-n", "--", "-", "|", "::"} {
		if slices.Contains(got, junk) {
			t.Errorf("纯符号词项 %q 不该入库：%v", junk, got)
		}
	}
	// 反向确认这条断言不是恒真的：正常词项必须在
	if !slices.Contains(got, "kubectl") {
		t.Fatalf("正常词项 kubectl 反而没了，说明断言前提不成立：%v", got)
	}
}
