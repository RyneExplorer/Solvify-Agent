package entity

import "time"

// SyncJob 映射同步任务表
type SyncJob struct {
	ID              string     `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	UserID          string     `gorm:"column:user_id;type:uuid;not null"`
	SyncSourceID    string     `gorm:"column:sync_source_id;type:uuid;not null"`
	KnowledgeBaseID string     `gorm:"column:knowledge_base_id;type:uuid;not null"`
	JobType         string     `gorm:"column:job_type;type:varchar(32);not null"`
	Status          int        `gorm:"column:status;not null;default:1"`
	TotalCount      int        `gorm:"column:total_count;not null;default:0"`
	SuccessCount    int        `gorm:"column:success_count;not null;default:0"`
	FailedCount     int        `gorm:"column:failed_count;not null;default:0"`
	ErrorMessage    string     `gorm:"column:error_message;not null;default:''"`
	StartedAt       *time.Time `gorm:"column:started_at"`
	FinishedAt      *time.Time `gorm:"column:finished_at"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
}

// TableName 返回同步任务表名
func (SyncJob) TableName() string {
	return "sync_jobs"
}
