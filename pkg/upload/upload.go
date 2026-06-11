package upload

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	baseDir                   = "uploads"
	maxImageSize              = 5 << 20
	defaultSubDir             = "common"
	permDir       os.FileMode = 0o755
)

var allowedImageTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// Result 表示一次上传成功后的结果
type Result struct {
	URL      string `json:"url"`
	Path     string `json:"path"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

// EnsureBaseDir 确保上传根目录存在，避免静态资源访问或保存文件时失败
func EnsureBaseDir() error {
	return os.MkdirAll(baseDir, permDir)
}

// SaveImage 校验并保存上传图片，返回可对外访问的地址
func SaveImage(c *gin.Context, field, category string) (*Result, error) {
	if err := EnsureBaseDir(); err != nil {
		return nil, fmt.Errorf("创建上传目录失败: %w", err)
	}

	fileHeader, err := c.FormFile(field)
	if err != nil {
		return nil, errors.New("请选择要上传的图片")
	}

	if fileHeader.Size <= 0 {
		return nil, errors.New("上传文件不能为空")
	}
	if fileHeader.Size > maxImageSize {
		return nil, errors.New("图片大小不能超过 5MB")
	}

	contentType, err := detectContentType(fileHeader)
	if err != nil {
		return nil, err
	}

	ext, ok := allowedImageTypes[contentType]
	if !ok {
		return nil, errors.New("仅支持 jpg、png、gif、webp 格式图片")
	}

	safeCategory := sanitizeCategory(category)
	targetDir := filepath.Join(baseDir, safeCategory)
	if err := os.MkdirAll(targetDir, permDir); err != nil {
		return nil, fmt.Errorf("创建分类目录失败: %w", err)
	}

	filename, err := buildFilename(ext)
	if err != nil {
		return nil, fmt.Errorf("生成文件名失败: %w", err)
	}

	relativePath := filepath.ToSlash(filepath.Join(safeCategory, filename))
	fullPath := filepath.Join(baseDir, safeCategory, filename)
	if err := c.SaveUploadedFile(fileHeader, fullPath); err != nil {
		return nil, fmt.Errorf("保存图片失败: %w", err)
	}

	return &Result{
		URL:      "/" + filepath.ToSlash(filepath.Join(baseDir, relativePath)),
		Path:     fullPath,
		Filename: filename,
		Size:     fileHeader.Size,
	}, nil
}

func sanitizeCategory(category string) string {
	category = strings.TrimSpace(category)
	if category == "" {
		return defaultSubDir
	}

	var b strings.Builder
	b.Grow(len(category))
	for _, r := range category {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		}
	}

	if b.Len() == 0 {
		return defaultSubDir
	}
	return b.String()
}

func detectContentType(fileHeader *multipart.FileHeader) (string, error) {
	file, err := fileHeader.Open()
	if err != nil {
		return "", fmt.Errorf("读取上传文件失败: %w", err)
	}
	defer file.Close()

	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && !errors.Is(err, multipart.ErrMessageTooLarge) && !errors.Is(err, os.ErrClosed) && !errors.Is(err, http.ErrMissingFile) {
		// 这里允许类似 EOF 的短读场景，只在真正的 I/O 异常时返回错误。
		if !errors.Is(err, os.ErrNotExist) && err.Error() != "EOF" {
			return "", fmt.Errorf("检测文件类型失败: %w", err)
		}
	}

	contentType := http.DetectContentType(buffer[:n])
	return contentType, nil
}

func buildFilename(ext string) (string, error) {
	randomBytes := make([]byte, 6)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%s%s", time.Now().Format("20060102150405"), hex.EncodeToString(randomBytes), ext), nil
}
