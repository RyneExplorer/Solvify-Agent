package entity

import (
	"time"

	"gorm.io/datatypes"
)

// UserPreference 用户偏好：常用模型、常用知识库、回答风格、是否自动深度模式
type UserPreference struct {
	ID                 string         `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	UserID             string         `gorm:"column:user_id;type:uuid;not null;uniqueIndex;comment:用户ID，一人一条" json:"user_id"`
	DefaultModelID     string         `gorm:"column:default_model_id;type:varchar(36);comment:默认使用的模型ID（系统模型表ID，可空）" json:"default_model_id"`
	PreferredKBIDs     datatypes.JSON `gorm:"column:preferred_kb_ids;type:jsonb;comment:常用知识库ID列表，JSON array[string]" json:"preferred_kb_ids"`
	AnswerStyle        string         `gorm:"column:answer_style;type:varchar(20);default:balanced;comment:回答风格：concise(简洁)/balanced(平衡)/detailed(详细)/step_by_step(分步)" json:"answer_style"`
	AutoDeepMode       bool           `gorm:"column:auto_deep_mode;default:false;comment:是否自动切深度模式（true=复杂问题自动切；false=始终用户手动）" json:"auto_deep_mode"`
	AutoDeepThreshold  int            `gorm:"column:auto_deep_threshold;default:2;comment:自动切深度模式的信号阈值，越大越不容易切（1~5）" json:"auto_deep_threshold"`
	UseMarkdownTable   bool           `gorm:"column:use_markdown_table;default:true;comment:回答尽量用表格呈现结构化数据" json:"use_markdown_table"`
	CitationStyle      string         `gorm:"column:citation_style;type:varchar(20);default:section_title;comment:引用格式：none(不标)/section_title(章节标题)/doc_title_only(仅文档名)" json:"citation_style"`
	CreatedAt          time.Time      `gorm:"column:created_at" json:"created_at"`
	UpdatedAt          time.Time      `gorm:"column:updated_at" json:"updated_at"`
}

func (*UserPreference) TableName() string {
	return "user_preferences"
}
