package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"

	"solvify-agent/pkg/config"
)

const (
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorReset  = "\033[0m"
)

var (
	log   *zap.Logger
	sugar *zap.SugaredLogger
)

func init() {
	log = zap.NewNop()
	sugar = log.Sugar()
}

// CustomLevelEncoder 自定义日志级别编码器，支持控制台颜色显示
func CustomLevelEncoder(level zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	var color string
	switch level {
	case zapcore.DebugLevel:
		color = colorBlue
	case zapcore.InfoLevel:
		color = colorGreen
	case zapcore.WarnLevel:
		color = colorYellow
	case zapcore.ErrorLevel:
		color = colorRed
	default:
		color = colorReset
	}
	enc.AppendString(color + level.CapitalString() + colorReset)
}

// Init 初始化日志系统
func Init(cfg *config.LogConfig) error {
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	level := parseLevel(cfg.Level)
	fileWriter := &lumberjack.Logger{
		Filename:   cfg.Filename,
		MaxSize:    cfg.MaxSize,
		MaxBackups: cfg.MaxBackups,
		MaxAge:     cfg.MaxAge,
		Compress:   cfg.Compress,
	}

	fileCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.AddSync(fileWriter),
		level,
	)

	consoleEncoderConfig := encoderConfig
	consoleEncoderConfig.EncodeLevel = CustomLevelEncoder
	consoleCore := zapcore.NewCore(
		zapcore.NewConsoleEncoder(consoleEncoderConfig),
		zapcore.AddSync(os.Stdout),
		level,
	)

	core := zapcore.NewTee(fileCore, consoleCore)
	log = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1), zap.AddStacktrace(zapcore.ErrorLevel))
	sugar = log.Sugar()
	return nil
}

// InitFromConfig 从全局配置初始化日志
func InitFromConfig() error {
	cfg := config.Get().Log
	return Init(&cfg)
}

// InitDefault 使用默认配置初始化日志
func InitDefault() error {
	cfg := &config.LogConfig{
		Level:      "debug",
		Filename:   "logs/app.log",
		MaxSize:    100,
		MaxBackups: 3,
		MaxAge:     28,
		Compress:   true,
	}
	return Init(cfg)
}

// Debug 打印调试日志
func Debug(msg string, fields ...zap.Field) {
	log.Debug(msg, fields...)
}

// Info 打印信息日志
func Info(msg string, fields ...zap.Field) {
	log.Info(msg, fields...)
}

// Warn 打印警告日志
func Warn(msg string, fields ...zap.Field) {
	log.Warn(msg, fields...)
}

// Error 打印错误日志
func Error(msg string, fields ...zap.Field) {
	log.Error(msg, fields...)
}

// Fatal 打印致命错误日志并退出进程
func Fatal(msg string, fields ...zap.Field) {
	log.Fatal(msg, fields...)
}

// Debugf 打印格式化调试日志
func Debugf(format string, args ...any) {
	sugar.Debugf(format, args...)
}

// Infof 打印格式化信息日志
func Infof(format string, args ...any) {
	sugar.Infof(format, args...)
}

// Warnf 打印格式化警告日志
func Warnf(format string, args ...any) {
	sugar.Warnf(format, args...)
}

// Errorf 打印格式化错误日志
func Errorf(format string, args ...any) {
	sugar.Errorf(format, args...)
}

// Fatalf 打印格式化致命错误日志并退出进程
func Fatalf(format string, args ...any) {
	sugar.Fatalf(format, args...)
}

// With 创建带固定字段的 logger
func With(fields ...zap.Field) *zap.Logger {
	return log.With(fields...)
}

// Sync 同步日志缓冲区
func Sync() error {
	return log.Sync()
}

// GetLogger 获取原始 zap logger
func GetLogger() *zap.Logger {
	return log
}

// GetSugaredLogger 获取 sugared logger
func GetSugaredLogger() *zap.SugaredLogger {
	return sugar
}

// parseLevel 将配置中的日志级别转换为 zap 级别
func parseLevel(level string) zapcore.Level {
	switch level {
	case "debug":
		return zapcore.DebugLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}
