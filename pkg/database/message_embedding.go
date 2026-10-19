package database

import (
	"context"
	"fmt"
	"gorm.io/gorm"
)

// CheckMessageEmbeddingSchema 只检查显式升级结果，不扫描消息或自动修改业务表。
func CheckMessageEmbeddingSchema(ctx context.Context, db *gorm.DB) error {
	var count int64
	if err := db.WithContext(ctx).Raw(`SELECT count(*) FROM pg_attribute WHERE attrelid=to_regclass('chat_messages') AND NOT attisdropped
 AND attname IN ('embedding_status','embedding_attempts','embedding_available_at','embedding_lease_until','embedding_lease_token','embedding_error')`).Scan(&count).Error; err != nil {
		return fmt.Errorf("检查消息向量任务表结构失败: %w", err)
	}
	if count != 6 {
		return fmt.Errorf("消息向量任务字段缺失，请先执行 scripts/migrations/20261017_message_embedding_jobs.sql")
	}
	return nil
}
