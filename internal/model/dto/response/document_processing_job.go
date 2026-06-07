package response

import "time"

// DocumentProcessingJobResponse 描述文档处理任务响应
type DocumentProcessingJobResponse struct {
	ID           string     `json:"id"`
	DocumentID   string     `json:"document_id"`
	JobType      string     `json:"job_type"`
	Status       int16      `json:"status"`
	ErrorMessage string     `json:"error_message"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}
