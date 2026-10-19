package tokenutil

import (
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestContextTokenCountingAtInputLimit(t *testing.T) {
	if os.Getenv("SOLVIFY_CONTEXT_STRESS_TEST") != "1" {
		t.Skip("显式启用24并发输入边界token计数性能测试")
	}
	text := string([]rune(strings.Repeat("连续中文输入上下文", 500))[:4000])
	expected := CountTokens(text, "gpt-4o")
	start := make(chan struct{})
	durations := make(chan time.Duration, 24)
	counts := make(chan int, 24)
	var workers sync.WaitGroup
	started := time.Now()
	for worker := 0; worker < 24; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			before := time.Now()
			counts <- CountTokens(text, "gpt-4o")
			durations <- time.Since(before)
		}()
	}
	close(start)
	workers.Wait()
	close(counts)
	close(durations)
	for count := range counts {
		if count != expected {
			t.Fatalf("并发计数不一致：%d/%d", count, expected)
		}
	}
	var elapsed []time.Duration
	for duration := range durations {
		elapsed = append(elapsed, duration)
	}
	sort.Slice(elapsed, func(first, second int) bool { return elapsed[first] < elapsed[second] })
	t.Logf("4000连续中文字符，24并发，token=%d，总耗时=%s p50=%s p95=%s 最大=%s；本机指标不是生产SLO", expected, time.Since(started), elapsed[11], elapsed[22], elapsed[23])
}
