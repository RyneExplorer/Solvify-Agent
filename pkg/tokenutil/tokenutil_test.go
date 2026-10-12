package tokenutil

import (
	"strings"
	"testing"
)

func TestUnknownModelUsesDefaultBPEForCountAndTruncation(t *testing.T) {
	// 先明确验证编码已可用，避免将两次字符估算相同当作 BPE 回退成功。
	encoding, err := getEncoding(defaultEncoding)
	if err != nil || encoding == nil {
		t.Fatal("默认 BPE 编码不可用")
	}
	text := strings.Repeat("record: alpha=7; project=starfish; budget=4096.\n", 200)
	want := len(encoding.Encode(text, nil, nil))
	name := "unregistered-context-accept-model"
	if got := CountTokens(text, name); got != want {
		t.Fatalf("未知模型未使用默认 BPE: got=%d want=%d", got, want)
	}
	cut, used := TruncateByTokens(text, name, 256)
	if cut == text || used > 256 || CountTokens(cut, name) != used || len(encoding.Encode(cut, nil, nil)) > 256 {
		t.Fatal("截断与计数口径不一致")
	}
}
