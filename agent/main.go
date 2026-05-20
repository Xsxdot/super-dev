// Package main 是 SuperDev agent 的启动入口。
//
// 职责：
//   - 解析命令行标志（--addr 监听地址，--data 数据目录）
//   - 创建和启动 HTTP API 服务
//   - 管理应用生命周期
//
// 边界：
//   - 不处理具体的 HTTP 路由逻辑，由 api 包提供
//   - 不包含业务规则，只负责进程启动和参数验证
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/superdev/agent/api"
)

func main() {
	addr := flag.String("addr", ":27017", "HTTP listen address")
	dataDir := flag.String("data", defaultDataDir(), "Data directory for logs.db and projects.json")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatal("create data dir:", err)
	}

	app, err := api.NewApp(api.AppConfig{DataDir: *dataDir})
	if err != nil {
		log.Fatal("create app:", err)
	}
	defer app.Close()

	fmt.Printf("SuperDev agent listening on %s\n", *addr)
	log.Fatal(app.Start(*addr))
}

// defaultDataDir 返回默认的数据目录路径（~/.superdev）。
func defaultDataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".superdev")
}
