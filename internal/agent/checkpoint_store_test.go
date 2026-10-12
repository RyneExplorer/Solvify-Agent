package agent

import (
	"context"
	"testing"
)

func TestInMemoryCheckpointOwnsStoredBytes(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryCheckPointStore()
	input := []byte("checkpoint")
	if err := store.Set(ctx, "id", input); err != nil {
		t.Fatal(err)
	}
	input[0] = 'X'
	got, found, err := store.Get(ctx, "id")
	if err != nil || !found || string(got) != "checkpoint" {
		t.Fatalf("写入后被调用方修改: %q found=%v err=%v", got, found, err)
	}
	got[0] = 'Y'
	got, _, _ = store.Get(ctx, "id")
	if string(got) != "checkpoint" {
		t.Fatalf("读取结果泄露了存储内存: %q", got)
	}
	if err := store.Delete(ctx, "id"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := store.Get(ctx, "id"); found {
		t.Fatal("删除后仍然存在")
	}
}
