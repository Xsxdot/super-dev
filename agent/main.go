// Package main 是 SuperDev agent 的启动入口。
//
// 职责：
//   - 解析命令行标志（--addr 监听地址，--data 数据目录，--install-binaries 安装二进制目录）
//   - 创建和启动 HTTP API 服务
//   - 管理应用生命周期
//   - `mcp` 子命令：分派到 agent/mcp.RunStdioMain 以 stdio MCP server 运行，
//     使远端机器仅靠这一个二进制即可完成编程智能体接入
//
// 边界：
//   - 不处理具体的 HTTP 路由逻辑，由 api 包提供
//   - 不包含业务规则，只负责进程启动和参数验证
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/xsxdot/super-dev/agent/api"
	"github.com/xsxdot/super-dev/agent/hostpaths"
	"github.com/xsxdot/super-dev/agent/mcp"
)

func main() {
	// mcp 子命令：以 stdio MCP server 运行（与独立 superdev-mcp 二进制同一实现）。
	// 远端机器只有 agent 一个二进制，MCP 配置写 `superdev-agent mcp` 即可接入，
	// 无需分发第二个二进制——这是远端接入设计的关键前置。
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		log.SetOutput(os.Stderr)
		if err := mcp.RunStdioMain(context.Background(), os.Stdin, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}

	addr := flag.String("addr", ":57017", "HTTP listen address")
	dataDir := flag.String("data", defaultDataDir(), "Data directory for logs.db and projects.json")
	installBinariesDir := flag.String("install-binaries", "", "Directory containing remote install agent binaries")
	sampleBinary := flag.String("sample-binary", "", "Path to bundled onboarding sample service binary")
	bootstrapToken := flag.String("bootstrap-token", "", "One-time bootstrap token for first security provision")
	requireAuth := flag.Bool("require-auth", false, "Deprecated: authentication is always on; kept for old install scripts")
	tlsCertFile := flag.String("tls-cert-file", "", "HTTPS certificate file for manually managed TLS")
	tlsKeyFile := flag.String("tls-key-file", "", "HTTPS private key file for manually managed TLS")
	flag.Parse()

	// --require-auth 已无实际作用（withSecurity 恒定校验），但远端安装脚本/systemd unit
	// 仍会传它——静默吞掉会让人以为它还管事，打一条废弃日志明示。
	if *requireAuth {
		log.Printf("[SuperDev] --require-auth 已废弃：鉴权现恒定开启，该参数被忽略（仅保留兼容旧安装脚本）")
	}

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatal("create data dir:", err)
	}

	app, err := api.NewApp(api.AppConfig{
		DataDir:          *dataDir,
		InstallBinaryDir: *installBinariesDir,
		SampleBinaryPath: *sampleBinary,
		BootstrapToken:   *bootstrapToken,
		RequireAuth:      *requireAuth,
		TLSCertFile:      *tlsCertFile,
		TLSKeyFile:       *tlsKeyFile,
	})
	if err != nil {
		log.Fatal("create app:", err)
	}

	// 监听 SIGTERM/SIGINT：桌面端退出会先发 SIGTERM，agent 借此主动停掉所有
	// 托管服务（app.Close 内含 procMgr.StopAll）再退出，避免遗留孤儿进程。
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	errCh := make(chan error, 1)
	go func() {
		log.Printf("SuperDev agent listening on %s", *addr)
		errCh <- app.Start(*addr)
	}()

	notifyCh := make(chan struct{}, 1)
	go func() {
		<-sigCh
		notifyCh <- struct{}{}
	}()

	reason, startErr := waitForShutdown(notifyCh, errCh)
	switch reason {
	case shutdownBySignal:
		log.Printf("SuperDev agent received shutdown signal, stopping managed services")
	case shutdownByServerExit:
		log.Printf("SuperDev agent server exited: %v", startErr)
	}

	app.Close()
	log.Printf("SuperDev agent stopped")

	if reason == shutdownByServerExit && startErr != nil {
		os.Exit(1)
	}
}

// defaultDataDir 返回默认的数据目录路径（~/.superdev）。
func defaultDataDir() string {
	home, _ := hostpaths.UserHome()
	return filepath.Join(home, ".superdev")
}

// shutdownReason 标识 agent 退出的触发来源，用于日志归因与退出码决策。
type shutdownReason int

const (
	// shutdownBySignal 表示收到 SIGTERM/SIGINT，由桌面端退出或用户主动停止触发。
	shutdownBySignal shutdownReason = iota
	// shutdownByServerExit 表示 HTTP server 自行返回（多为监听失败）。
	shutdownByServerExit
)

// waitForShutdown 阻塞等待「收到退出信号」或「server 退出」中先到的一个。
//
// 参数：
//   - sigCh: 收到退出信号时会有一个事件；
//   - errCh: server 退出时投递其返回值（可能为 nil）。
//
// 返回：
//   - reason: 触发退出的来源；
//   - err: 仅 server 退出路径携带其原始错误，信号路径恒为 nil。
//
// 注意：两个事件可能几乎同时到达，select 任取其一即可，后续 app.Close 幂等。
func waitForShutdown(sigCh <-chan struct{}, errCh <-chan error) (shutdownReason, error) {
	select {
	case <-sigCh:
		return shutdownBySignal, nil
	case err := <-errCh:
		return shutdownByServerExit, err
	}
}
