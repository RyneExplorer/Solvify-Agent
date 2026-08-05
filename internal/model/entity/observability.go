package entity

import (
	"time"

	"gorm.io/datatypes"
)

type ChatTrace struct {
	ID         string    `gorm:"primaryKey;type:varchar(128)" json:"id"`
	RequestID  string    `gorm:"index;type:varchar(128)" json:"request_id,omitempty"`
	UserID     string    `gorm:"index;type:varchar(64)" json:"user_id,omitempty"`
	SessionID  string    `gorm:"index;type:varchar(64)" json:"session_id,omitempty"`
	SampleRate float64   `gorm:"default:0" json:"sample_rate,omitempty"`
	Sampled    bool      `gorm:"default:false" json:"sampled"`
	DurationMs int64     `gorm:"default:0" json:"duration_ms,omitempty"`
	Status     string    `gorm:"type:varchar(32)" json:"status,omitempty"`
	Error      string    `gorm:"type:text" json:"error,omitempty"`
	Attrs      datatypes.JSON `gorm:"type:jsonb" json:"attrs,omitempty"`
	SpanTree   datatypes.JSON `gorm:"type:jsonb" json:"span_tree,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func (ChatTrace) TableName() string { return "chat_traces" }

type AgentTask struct {
	ID              string    `gorm:"primaryKey;type:varchar(64)" json:"id"`
	TraceID         string    `gorm:"index;type:varchar(128)" json:"trace_id,omitempty"`
	SessionID       string    `gorm:"index;type:varchar(64)" json:"session_id,omitempty"`
	UserID          string    `gorm:"index;type:varchar(64)" json:"user_id,omitempty"`
	ModelID         string    `gorm:"type:varchar(128)" json:"model_id,omitempty"`
	SearchMode      string    `gorm:"type:varchar(32)" json:"search_mode,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	TotalSteps      int       `gorm:"default:0" json:"total_steps,omitempty"`
	ToolCalls       int       `gorm:"default:0" json:"tool_calls,omitempty"`
	Status          string    `gorm:"type:varchar(32)" json:"status,omitempty"`
	AbortReason     string    `gorm:"type:varchar(128)" json:"abort_reason,omitempty"`
	TokensPrompt    int       `gorm:"default:0" json:"tokens_prompt,omitempty"`
	TokensCompletion int      `gorm:"default:0" json:"tokens_completion,omitempty"`
	TotalCost       float64   `gorm:"default:0" json:"total_cost,omitempty"`
	ErrorSummary    string    `gorm:"type:text" json:"error_summary,omitempty"`
	FeedbackRating  *int      `gorm:"default:null" json:"feedback_rating,omitempty"`
}

func (AgentTask) TableName() string { return "agent_tasks" }

type AgentTaskStep struct {
	ID                string    `gorm:"primaryKey;type:varchar(64)" json:"id"`
	TaskID            string    `gorm:"index;type:varchar(64);not null" json:"task_id"`
	StepIndex         int       `gorm:"not null;default:0" json:"step_index"`
	StartedAt         time.Time `json:"started_at"`
	EndedAt           *time.Time `json:"ended_at,omitempty"`
	ThinkingSummary   string    `gorm:"type:text" json:"thinking_summary,omitempty"`
	ToolName          string    `gorm:"type:varchar(128)" json:"tool_name,omitempty"`
	ToolInputMasked   string    `gorm:"type:text" json:"tool_input_masked,omitempty"`
	ToolResultSummary string    `gorm:"type:text" json:"tool_result_summary,omitempty"`
	ToolStatus        string    `gorm:"type:varchar(32)" json:"tool_status,omitempty"`
	ToolError         string    `gorm:"type:text" json:"tool_error,omitempty"`
	LatencyMs         int64     `gorm:"default:0" json:"latency_ms,omitempty"`
	TokensDelta       int       `gorm:"default:0" json:"tokens_delta,omitempty"`
	Attrs             datatypes.JSON `gorm:"type:jsonb" json:"attrs,omitempty"`
}

func (AgentTaskStep) TableName() string { return "agent_task_steps" }
