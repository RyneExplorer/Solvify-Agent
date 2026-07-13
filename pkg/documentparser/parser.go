package documentparser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	apperrors "solvify-agent/pkg/errors"
)

var textFileTypes = map[string]struct{}{
	"txt": {}, "md": {}, "markdown": {}, "html": {}, "csv": {}, "json": {},
}

var pythonFileTypes = map[string]struct{}{
	"docx": {}, "pdf": {}, "pptx": {},
}

// Config 描述文档文本解析器配置
type Config struct {
	PythonPath     string
	ScriptPath     string
	TimeoutSeconds int
}

// TextExtractor 定义文档正文提取能力
type TextExtractor interface {
	Supports(fileType string) bool
	Extract(ctx context.Context, path, fileType string) (string, error)
	ExtractBytes(ctx context.Context, content []byte, fileType string) (string, error)
}

type textExtractor struct {
	cfg Config
}

// New 创建文档文本解析器
func New(cfg Config) TextExtractor {
	if strings.TrimSpace(cfg.PythonPath) == "" {
		cfg.PythonPath = "python"
	}
	if strings.TrimSpace(cfg.ScriptPath) == "" {
		cfg.ScriptPath = filepath.Join("pkg", "documentparser", "python", "parse_document.py")
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 30
	}
	return &textExtractor{cfg: cfg}
}

// Supports 判断文件类型是否支持正文提取
func (e *textExtractor) Supports(fileType string) bool {
	fileType = normalizeFileType(fileType)
	if _, ok := textFileTypes[fileType]; ok {
		return true
	}
	_, ok := pythonFileTypes[fileType]
	return ok
}

// Extract 从文件路径提取正文
func (e *textExtractor) Extract(ctx context.Context, path, fileType string) (string, error) {
	fileType = normalizeFileType(fileType)
	if _, ok := textFileTypes[fileType]; ok {
		contentBytes, err := os.ReadFile(path)
		if err != nil {
			return "", apperrors.NewWithErr(apperrors.CodeInternalError, "读取文档文件失败", err)
		}
		return string(contentBytes), nil
	}
	if _, ok := pythonFileTypes[fileType]; ok {
		return e.extractWithPython(ctx, path, fileType)
	}
	return "", apperrors.New(apperrors.CodeDocumentStatusInvalid, "当前文件类型暂不支持自动解析")
}

// ExtractBytes 从内存内容提取正文
func (e *textExtractor) ExtractBytes(ctx context.Context, content []byte, fileType string) (string, error) {
	fileType = normalizeFileType(fileType)
	if _, ok := textFileTypes[fileType]; ok {
		return string(content), nil
	}
	if _, ok := pythonFileTypes[fileType]; ok {
		tempFile, err := os.CreateTemp("", "solvify-parse-*."+fileType)
		if err != nil {
			return "", apperrors.NewWithErr(apperrors.CodeInternalError, "创建解析临时文件失败", err)
		}
		tempPath := tempFile.Name()
		defer os.Remove(tempPath)
		if _, err := tempFile.Write(content); err != nil {
			_ = tempFile.Close()
			return "", apperrors.NewWithErr(apperrors.CodeInternalError, "写入解析临时文件失败", err)
		}
		if err := tempFile.Close(); err != nil {
			return "", apperrors.NewWithErr(apperrors.CodeInternalError, "关闭解析临时文件失败", err)
		}
		return e.extractWithPython(ctx, tempPath, fileType)
	}
	return "", apperrors.New(apperrors.CodeDocumentStatusInvalid, "当前文件类型暂不支持自动解析")
}

func (e *textExtractor) extractWithPython(ctx context.Context, path, fileType string) (string, error) {
	if _, err := os.Stat(e.cfg.ScriptPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", apperrors.New(apperrors.CodeInternalError, "文档解析脚本不存在")
		}
		return "", apperrors.NewWithErr(apperrors.CodeInternalError, "检查文档解析脚本失败", err)
	}

	timeout := time.Duration(e.cfg.TimeoutSeconds) * time.Second
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, e.cfg.PythonPath, e.cfg.ScriptPath, "--type", fileType, "--file", path)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return "", apperrors.New(apperrors.CodeInternalError, "文档解析超时")
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", apperrors.New(apperrors.CodeInternalError, fmt.Sprintf("文档解析失败: %s", message))
	}

	text := strings.TrimSpace(stdout.String())
	if text == "" {
		return "", apperrors.New(apperrors.CodeDocumentStatusInvalid, "文档未提取到可用文本")
	}
	return text, nil
}

func normalizeFileType(fileType string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(fileType)), ".")
}
