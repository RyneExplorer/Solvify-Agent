package tokenutil

import (
	"crypto/sha256"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestTokenCountCacheSharesConcurrentCalculation(t *testing.T) {
	c := newTokenCountCache(2)
	key := tokenCountKey{content: sha256.Sum256([]byte("同一份原文"))}
	var calls atomic.Int32
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 24 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			if got := c.count(key, func() int { calls.Add(1); return 137 }); got != 137 {
				t.Errorf("并发计数不一致：%d", got)
			}
		}()
	}
	close(start)
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatalf("同一原文重复计算：%d", calls.Load())
	}
}

func TestTokenCountCacheKeepsEncodingScopeAndEvicts(t *testing.T) {
	first, _ := getEncoding("cl100k_base")
	second, _ := getEncoding("o200k_base")
	if first == nil || second == nil || first == second {
		t.Fatal("测试需要两种不同的真实编码")
	}
	c := newTokenCountCache(2)
	key := tokenCountKey{encoding: first, content: sha256.Sum256([]byte("原文"))}
	other := tokenCountKey{encoding: second, content: key.content}
	if c.count(key, func() int { return 11 }) != 11 || c.count(other, func() int { return 19 }) != 19 {
		t.Fatal("不同编码错误共享计数")
	}
	_, _ = c.get(key) // 最近使用的项必须保留。
	third := tokenCountKey{encoding: first, content: sha256.Sum256([]byte("不同原文"))}
	c.count(third, func() int { return 23 })
	if _, found := c.get(other); found {
		t.Fatal("缓存未淘汰最久未使用项")
	}
	if count, found := c.get(key); !found || count != 11 || len(c.entries) != 2 {
		t.Fatal("淘汰破坏现有计数或超过容量")
	}
}

func TestCachedCountsAndTruncationMatchExactBPE(t *testing.T) {
	for _, name := range []string{"cl100k_base", "gpt-4o"} {
		t.Run(name, func(t *testing.T) {
			encoding, _ := getEncoding(name)
			if encoding == nil {
				t.Fatal("真实BPE编码不可用")
			}
			for _, text := range []string{strings.Repeat("检索结果内容", 80), "金额129.95；URL=https://example.test/a?x=1\n```go\nx := 7\n```", "<|endoftext|>🙂中文\t\n"} {
				want := len(encoding.Encode(text, nil, nil))
				for range 2 {
					if got := CountTokens(text, name); got != want {
						t.Fatalf("缓存改变真实计数：%d/%d", got, want)
					}
				}
				if cut, used := TruncateByTokens(text, name, want); cut != text || used != want {
					t.Fatal("命中缓存后错误截断完整原文")
				}
				cut, used := TruncateByTokens(text, name, max(1, want/2))
				if used > max(1, want/2) || len(encoding.Encode(cut, nil, nil)) > max(1, want/2) {
					t.Fatal("缓存导致截断越过真实预算")
				}
			}
		})
	}
}

func BenchmarkTokenCountRepeatedChinese(b *testing.B) {
	text := strings.Repeat("检索结果内容", 800)
	encoding, _ := getEncoding("gpt-4o")
	if encoding == nil {
		b.Fatal("真实BPE编码不可用")
	}
	b.Run("direct", func(b *testing.B) {
		for b.Loop() {
			_ = encoding.Encode(text, nil, nil)
		}
	})
	CountTokens(text, "gpt-4o")
	b.Run("cached", func(b *testing.B) {
		for b.Loop() {
			CountTokens(text, "gpt-4o")
		}
	})
}
