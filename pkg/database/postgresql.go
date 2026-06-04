package database

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

// OpenPostgreSQL 初始化 PostgreSQL 连接
func OpenPostgreSQL(cfg *config.PostgresConfig) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(buildPostgreSQLDSN(cfg)), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("连接 PostgreSQL 失败: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("获取 PostgreSQL 连接池失败: %w", err)
	}

	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetimeMinutes) * time.Minute)

	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("检查 PostgreSQL 连接失败: %w", err)
	}

	if cfg.EnablePGVector {
		if err := enablePGVector(db); err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
	}

	logger.Info("PostgreSQL 连接成功",
		zap.String("host", cfg.Host),
		zap.Int("port", cfg.Port),
		zap.String("database", cfg.Database),
		zap.String("user", cfg.Username),
	)
	return db, nil
}

// ClosePostgreSQL 关闭 PostgreSQL 连接
func ClosePostgreSQL(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("获取 PostgreSQL 连接池失败: %w", err)
	}
	if err := sqlDB.Close(); err != nil {
		return fmt.Errorf("关闭 PostgreSQL 连接失败: %w", err)
	}
	return nil
}

// buildPostgreSQLDSN 生成 PostgreSQL 连接地址
func buildPostgreSQLDSN(cfg *config.PostgresConfig) string {
	values := url.Values{}

	dsn := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.Username, cfg.Password),
		Host:     net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:     cfg.Database,
		RawQuery: values.Encode(),
	}
	return dsn.String()
}

// enablePGVector 启用 pgvector 扩展
func enablePGVector(db *gorm.DB) error {
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		return fmt.Errorf("启用 pgvector 扩展失败: %w", err)
	}
	logger.Info("pgvector 扩展检查完成")
	return nil
}
