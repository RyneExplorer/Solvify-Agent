package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"solvify-agent/pkg/config"
	"solvify-agent/pkg/database"
	"solvify-agent/pkg/logger"
)

func main() {
	var (
		configPath = flag.String("config", "configs/config.yaml", "配置文件路径")
		dryRun     = flag.Bool("dry-run", false, "仅打印要执行的 SQL，不真正运行")
	)
	flag.Parse()

	files := flag.Args()
	if len(files) == 0 {
		fmt.Println("用法: go run cmd/migrate/main.go [-config=...] [-dry-run] <sql 文件1> [sql 文件2] ...")
		os.Exit(1)
	}

	// 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Printf("加载配置失败: %v\n", err)
		os.Exit(1)
	}

	// 初始化日志
	logger.Init(&cfg.Log)

	// 连接 PostgreSQL
	db, err := database.OpenPostgreSQL(&cfg.Database.Postgres)
	if err != nil {
		fmt.Printf("连接数据库失败: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = database.ClosePostgreSQL(db) }()

	sqlDB, err := db.DB()
	if err != nil {
		fmt.Printf("获取连接池失败: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	for _, file := range files {
		sql, err := os.ReadFile(file)
		if err != nil {
			fmt.Printf("读取 SQL 文件失败 %s: %v\n", file, err)
			os.Exit(1)
		}

		if *dryRun {
			fmt.Printf("\n--- %s (dry-run) ---\n%s\n", file, string(sql))
			continue
		}

		fmt.Printf("正在执行: %s\n", file)
		if isSelectQuery(string(sql)) {
			if err := queryAndPrint(ctx, sqlDB, string(sql)); err != nil {
				fmt.Printf("查询失败 %s: %v\n", file, err)
				os.Exit(1)
			}
		} else {
			if _, err := sqlDB.ExecContext(ctx, string(sql)); err != nil {
				fmt.Printf("执行 SQL 失败 %s: %v\n", file, err)
				os.Exit(1)
			}
		}
		fmt.Printf("完成: %s\n", file)
	}

	fmt.Println("\n所有 SQL 脚本执行完成")
}

func isSelectQuery(sql string) bool {
	trimmed := strings.TrimSpace(sql)
	// 跳过单行注释，找到第一个有效 token
	for strings.HasPrefix(trimmed, "--") {
		idx := strings.Index(trimmed, "\n")
		if idx < 0 {
			return false
		}
		trimmed = strings.TrimSpace(trimmed[idx+1:])
	}
	return strings.HasPrefix(strings.ToUpper(trimmed), "SELECT")
}

func queryAndPrint(ctx context.Context, db *sql.DB, query string) error {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return err
	}

	fmt.Println(strings.Join(columns, " | "))
	fmt.Println(strings.Repeat("-", 60))

	values := make([]interface{}, len(columns))
	valuePtrs := make([]interface{}, len(columns))
	for i := range values {
		valuePtrs[i] = &values[i]
	}

	for rows.Next() {
		if err := rows.Scan(valuePtrs...); err != nil {
			return err
		}
		for i, v := range values {
			if i > 0 {
				fmt.Print(" | ")
			}
			fmt.Printf("%v", v)
		}
		fmt.Println()
	}
	return rows.Err()
}
