package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"solvify-agent/internal/app"
	"syscall"
	"time"
)

func main() {
	databaseName := flag.String("database", "", "目标数据库名（必填，核对配置及.env覆盖）")
	limit := flag.Int("limit", 1000, "本次最多排队数量（1–10000）")
	retryFailed := flag.Bool("retry-failed", false, "同时重新登记已失败任务并重置次数")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	n, err := app.QueueHistoricalMessageEmbeddings(ctx, *databaseName, *limit, *retryFailed)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("已登记 %d 条消息向量任务，由运行中的服务异步处理。\n", n)
}
