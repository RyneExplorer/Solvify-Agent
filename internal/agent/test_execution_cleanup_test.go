package agent

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func TestGuardExecutionCancelsWhenTestFinishes(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	m := &deepTestModel{respond: func(ctx context.Context, _ []*schema.Message) (*schema.Message, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	}}
	var events <-chan Event
	t.Run("拥有执行生命周期的测试", func(t *testing.T) {
		events = mustExecute(t, guardTestContext(guardProbeTool{}), guardTestEngine(), Request{Profile: ProfileQuick, Query: "等待取消"}, m)
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("模型未启动")
		}
	})
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("测试结束后仍有未取消模型调用")
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-timer.C:
			t.Fatal("取消后执行未收尾")
		}
	}
}
