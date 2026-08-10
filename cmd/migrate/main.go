// Command migrate 执行数据库 schema 迁移（用 GORM AutoMigrate）。
// 用法: go run ./cmd/migrate
package main

import (
	"fmt"
	"log"

	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/database"
	"solvify-agent/pkg/logger"
)

func main() {
	_ = logger.InitDefault()

	cfg, err := config.Load("configs/config.yaml")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	db, err := database.OpenPostgreSQL(&cfg.Database.Postgres)
	if err != nil {
		log.Fatalf("连接 PostgreSQL 失败: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	fmt.Println("开始迁移...")

	// 迁移 ChatSession（自动补 pending_clarify / pending_checkpoint 列）
	if err := db.AutoMigrate(&entity.ChatSession{}); err != nil {
		log.Fatalf("迁移 ChatSession 失败: %v", err)
	}
	fmt.Println("✓ chat_sessions 已就绪")

	// 创建 agent_checkpoints 表
	if err := db.AutoMigrate(&entity.AgentCheckpoint{}); err != nil {
		log.Fatalf("迁移 AgentCheckpoint 失败: %v", err)
	}
	fmt.Println("✓ agent_checkpoints 已就绪")

	fmt.Println("迁移完成 ✅")
}
