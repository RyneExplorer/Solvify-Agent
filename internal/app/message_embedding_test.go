package app

import (
	"context"
	"solvify-agent/pkg/config"
	"testing"
	"time"
)

func TestMessageEmbeddingDisabledDoesNotAccessDependencies(t *testing.T) {
	cfg := config.Default()
	cfg.Database.Postgres.EnablePGVector = false
	a := &App{cfg: cfg}
	if err := a.startMessageEmbeddingWorker(); err != nil {
		t.Fatal(err)
	}
	if a.messageEmbeddingCancel != nil || a.messageEmbeddingDone != nil {
		t.Fatal("禁用时启动了后台任务")
	}
	a.stopMessageEmbeddingWorker()
}

func TestMessageEmbeddingStopWaitsForInFlightSettlement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	settle := make(chan struct{})
	a := &App{messageEmbeddingCancel: cancel, messageEmbeddingDone: make(chan struct{})}
	go func() { <-ctx.Done(); <-settle; close(a.messageEmbeddingDone) }()
	stopped := make(chan struct{})
	go func() { a.stopMessageEmbeddingWorker(); close(stopped) }()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("未取消任务")
	}
	select {
	case <-stopped:
		t.Fatal("未等待任务归还就释放依赖")
	default:
	}
	close(settle)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("未结束等待")
	}
}
