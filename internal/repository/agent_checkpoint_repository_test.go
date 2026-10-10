package repository

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

func TestCheckpointFindRejectsExpiredRowsBeforeCleanup(t *testing.T) {
	for _, test := range []struct {
		name   string
		expiry time.Time
		found  bool
	}{
		{"有效", time.Now().Add(time.Hour), true},
		{"过期但尚未清理", time.Now().Add(-time.Hour), false},
		{"缺少有效期", time.Time{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=localhost user=test dbname=test"}), &gorm.Config{DisableAutomaticPing: true, DryRun: true})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sqlDB.Close() })
			// 注入数据库返回的行，验证过期判断不依赖后台物理删除。
			if err := db.Callback().Query().Replace("gorm:query", func(tx *gorm.DB) {
				*tx.Statement.Dest.(*entity.AgentCheckpoint) = entity.AgentCheckpoint{ID: "cp", ExpiredAt: test.expiry, Checkpoint: []byte("saved")}
				tx.RowsAffected = 1
			}); err != nil {
				t.Fatal(err)
			}
			data, found, err := NewAgentCheckpointRepository(db).Find(context.Background(), "cp")
			if err != nil || found != test.found {
				t.Fatalf("found=%t err=%v", found, err)
			}
			if test.found && string(data) != "saved" || !test.found && len(data) != 0 {
				t.Fatalf("过期数据处理错误: %q", data)
			}
		})
	}
}
