// Package hostpaths 提供 agent 在服务环境下仍能正确解析的本机路径。
//
// 职责：
//   - 解析当前进程所属用户的 home 目录：先读 $HOME，缺失时回落到 passwd
//
// 边界：
//   - 不修改环境变量，不缓存结果
//   - 不把 HOME 写进 systemd/launchd 模板（那是 installer 的事）
//   - 不处理 ~user 展开，只解析「当前进程用户」的 home
package hostpaths

import (
	"fmt"
	"log"
	"os"
	"os/user"
)

// UserHome 解析当前进程所属用户的 home 目录。
//
// 返回：
//   - 绝对路径
//   - $HOME 与 passwd 都拿不到时，错误信息同时包含两条失败原因
//
// 注意：
//   - 优先 os.UserHomeDir()，跟随调用方环境（测试可 t.Setenv("HOME", ...)）
//   - 成功且未回落的路径不打日志，避免每次调用刷屏
func UserHome() (string, error) {
	home, envErr := os.UserHomeDir()
	if envErr == nil && home != "" {
		return home, nil
	}
	if envErr == nil {
		envErr = fmt.Errorf("$HOME is empty")
	}

	// systemd / launchd daemon 默认不注入 HOME，而这是 SuperDev 安装远端
	// agent 的唯一方式。只信 $HOME 会让 integrations / mcp-setup 等通道
	// 在真机上整片 500。agent 以 CGO_ENABLED=0 构建，user.Current 走纯 Go
	// 的 /etc/passwd 解析，不依赖 $HOME。
	u, passwdErr := user.Current()
	if passwdErr == nil && u != nil && u.HomeDir != "" {
		log.Printf("[SuperDev] hostpaths: $HOME 缺失（%v），已回落到 passwd，home=%s", envErr, u.HomeDir)
		return u.HomeDir, nil
	}
	if passwdErr == nil {
		passwdErr = fmt.Errorf("passwd home is empty")
	}
	err := fmt.Errorf("resolve home: env: %v; passwd: %v", envErr, passwdErr)
	log.Printf("[SuperDev] hostpaths: 解析 home 失败 %v", err)
	return "", err
}
