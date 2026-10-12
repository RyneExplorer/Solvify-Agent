package eventch

import (
	"context"
	"testing"
	"time"
)

// 这些用例把 Send 的三条不变量钉住：
//  1. ctx 已取消 → 立即返回，绝不阻塞（这是修 P0-1 永久阻塞的核心）；
//  2. ctx 未取消且无人消费 → 必须仍然阻塞（即它不是「无条件丢弃」，
//     正常运行时的事件一个都不能丢，否则 SSE 流会缺帧）；
//  3. 先前阻塞住的 Send，要在 ctx 取消的那一刻解除阻塞。

// ctx 已取消时，即使通道无缓冲且没有任何消费者，也必须立刻返回。
func TestSend_ReturnsImmediatelyWhenCtxAlreadyCancelled(t *testing.T) {
	ch := make(chan int) // 无缓冲、无消费者
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		Send(ctx, ch, 1)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ctx 已取消，Send 仍然阻塞")
	}
}

// 消费者就绪时必须正常投递（防止把 Send 写成无条件丢弃）。
func TestSend_DeliversWhenConsumerIsReady(t *testing.T) {
	ch := make(chan int, 1)
	Send(context.Background(), ch, 42)

	select {
	case got := <-ch:
		if got != 42 {
			t.Errorf("投递的值不对: got %d, want 42", got)
		}
	default:
		t.Fatal("ctx 未取消且通道有空位，事件却被丢弃")
	}
}

// ctx 未取消且无人消费时，Send 必须保持阻塞 —— 否则正常运行时会静默丢事件。
func TestSend_StillBlocksWhenCtxAliveAndNoConsumer(t *testing.T) {
	ch := make(chan int)

	done := make(chan struct{})
	go func() {
		Send(context.Background(), ch, 1)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("ctx 未取消、通道无人消费，Send 却返回了 —— 这会把正常路径的事件也丢掉")
	case <-time.After(80 * time.Millisecond):
	}
}

// 先阻塞，再取消 ctx，必须立刻解除阻塞。
func TestSend_UnblocksWhenCtxCancelledLater(t *testing.T) {
	ch := make(chan int)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		Send(ctx, ch, 1)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("没有任何消费者，Send 不应提前返回")
	case <-time.After(80 * time.Millisecond):
	}

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ctx 取消后 Send 没有解除阻塞")
	}
}
