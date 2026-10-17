package tool

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestToolExecutionContextGuards(t *testing.T) {
	ctx := context.Background()
	for _, duration := range []time.Duration{0, -time.Second} {
		if _, _, err := ExecutionContext(ctx, duration); err == nil {
			t.Fatal("无效策略不应放行")
		}
	}
	parent, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	run, stop, err := ExecutionContext(parent, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	parentDeadline, _ := parent.Deadline()
	runDeadline, _ := run.Deadline()
	if !parentDeadline.Equal(runDeadline) {
		t.Fatal("扩大上游预算")
	}
	cancel()
	if !errors.Is(ExecutionError(run, nil), context.Canceled) {
		t.Fatal("已取消调用不能返回成功")
	}
	if _, _, err = ExecutionContext(parent, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatal("上游取消后仍执行")
	}
}

func TestConfiguredToolTimeout(t *testing.T) {
	for _, seconds := range []int{-1, 86401} {
		if _, err := ConfiguredExecutionTimeout(seconds, time.Minute); err == nil {
			t.Fatal("非法时限被放行")
		}
	}
	for seconds, want := range map[int]time.Duration{0: time.Minute, 1: time.Second, 86400: 24 * time.Hour} {
		got, err := ConfiguredExecutionTimeout(seconds, time.Minute)
		if err != nil || got != want {
			t.Fatalf("%d: %v %v", seconds, got, err)
		}
	}
}
