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
	"log"
	"os"
	"path/filepath"

	"github.com/xsxdot/super-dev/agent/api"
)

func main() {
	addr := flag.String("addr", ":57017", "HTTP listen address")
	dataDir := flag.String("data", defaultDataDir(), "Data directory for logs.db and projects.json")
	sampleBinary := flag.String("sample-binary", "", "Path to bundled onboarding sample service binary")
	bootstrapToken := flag.String("bootstrap-token", "", "One-time bootstrap token for first security provision")
	requireAuth := flag.Bool("require-auth", false, "Require bearer token authentication for agent API")
	tlsCertFile := flag.String("tls-cert-file", "", "HTTPS certificate file for manually managed TLS")
	tlsKeyFile := flag.String("tls-key-file", "", "HTTPS private key file for manually managed TLS")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatal("create data dir:", err)
	}

	app, err := api.NewApp(api.AppConfig{
		DataDir:          *dataDir,
		SampleBinaryPath: *sampleBinary,
		BootstrapToken:   *bootstrapToken,
		RequireAuth:      *requireAuth,
		TLSCertFile:      *tlsCertFile,
		TLSKeyFile:       *tlsKeyFile,
	})
	if err != nil {
		log.Fatal("create app:", err)
	}
	defer app.Close()

	log.Printf("SuperDev agent listening on %s", *addr)
	log.Fatal(app.Start(*addr))
}

// defaultDataDir 返回默认的数据目录路径（~/.superdev）。
func defaultDataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".superdev")
}
