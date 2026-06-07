package entity

import "time"

// DocumentProcessingJob 映射文档处理任务表
type DocumentProcessingJob struct {
	ID           string     `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	UserID       string     `gorm:"column:user_id;type:uuid;not null"`
	DocumentID   string     `gorm:"column:document_id;type:uuid;not null"`
	JobType      string     `gorm:"column:job_type;size:32;not null"`
	Status       int16      `gorm:"column:status;not null;default:1"`
	ErrorMessage string     `gorm:"column:error_message;not null;default:''"`
	StartedAt    *time.Time `gorm:"column:started_at"`
	FinishedAt   *time.Time `gorm:"column:finished_at"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at"`
}

// TableName 返回文档处理任务表名
func (DocumentProcessingJob) TableName() string {
	return "document_processing_jobs"
}
