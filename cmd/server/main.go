package main

import (
	"fmt"

	"solvify-agent/internal/app"
)

func main() {
	application := app.NewApp()
	if err := application.Initialize(); err != nil {
		panic(fmt.Sprintf("应用初始化失败: %v", err))
	}
	application.Run()
}
