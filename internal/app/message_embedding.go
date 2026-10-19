package app

import (
	"context"
	"fmt"
	"solvify-agent/internal/llm"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/database"
)

func (a *App) startMessageEmbeddingWorker() error {
	if !a.cfg.Database.Postgres.EnablePGVector {
		return nil
	}
	// 后台任务直接使用可取消的客户端，避免共享singleflight先返回取消、内部请求仍访问依赖。
	client, err := llm.NewEmbeddingClientFromConfig(context.Background(), &a.cfg.Embedding)
	if err != nil {
		return fmt.Errorf("初始化消息向量客户端失败")
	}
	worker, err := service.NewMessageEmbeddingWorker(repository.NewMessageEmbeddingRepository(a.postgresqlDB), client.Embed, a.cfg.Embedding.MessageJobs, a.cfg.Embedding.Dimension)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.messageEmbeddingCancel = cancel
	a.messageEmbeddingDone = make(chan struct{})
	go func() { defer close(a.messageEmbeddingDone); worker.Run(ctx) }()
	return nil
}

func (a *App) stopMessageEmbeddingWorker() {
	if a.messageEmbeddingCancel != nil {
		a.messageEmbeddingCancel()
		<-a.messageEmbeddingDone
	}
}

// QueueHistoricalMessageEmbeddings 仅连接数据库排队，不启动HTTP、Agent或Embedding请求。
func QueueHistoricalMessageEmbeddings(ctx context.Context, expectedDatabase string, limit int, retryFailed bool) (int64, error) {
	cfg, err := config.Load("")
	if err != nil {
		return 0, fmt.Errorf("读取维护配置失败")
	}
	if expectedDatabase == "" || cfg.Database.Postgres.Database != expectedDatabase {
		return 0, fmt.Errorf("必须通过 --database 指定与实际配置一致的数据库名")
	}
	if !cfg.Database.Postgres.EnablePGVector {
		return 0, fmt.Errorf("当前配置未启用pgvector")
	}
	db, err := database.OpenPostgreSQL(&cfg.Database.Postgres)
	if err != nil {
		return 0, fmt.Errorf("连接维护数据库失败")
	}
	defer database.ClosePostgreSQL(db)
	if err = database.CheckMessageEmbeddingSchema(ctx, db); err != nil {
		return 0, err
	}
	return service.NewMessageEmbeddingBackfiller(repository.NewMessageEmbeddingRepository(db)).Enqueue(ctx, limit, retryFailed)
}
