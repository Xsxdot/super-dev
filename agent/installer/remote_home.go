// remote_home.go 在目标机上查询服务进程应注入的 HOME。
//
// 职责：
//   - 安装时通过已有的 remote.Run 通道查一次真实 home，填进 ServiceOptions.HomeDir
//   - getent passwd 取第 6 段；getent 不可用时退到 eval echo ~user
//
// 边界：
//   - 不修改 hostpaths；那边在 $HOME 缺失时回落 passwd 是对的
//   - 查不到时不中断安装，只 WARN 并保留惯例路径兜底
//   - Windows 服务模板不注入 HOME，本文件不查询
package installer

import (
	"context"
	"fmt"
	"log"
	"strings"
	"unicode"
)

func getentPasswdCommand(user string) string {
	return "getent passwd " + shellQuote(user)
}

func evalTildeHomeCommand(user string) string {
	// ~user 的用户名不能加引号，否则 shell 不做 tilde 展开。调用方必须先
	// isSafeUnixUser，禁止把任意 SSHUser 拼进 eval。
	return "eval echo ~" + user
}

// fillServiceHomeDir 在生成 systemd/launchd 模板前，把目标用户的真实 home
// 写入 opts.HomeDir。
//
// 只猜 /home/<user> 会把错误 HOME 注入服务环境：os.UserHomeDir 会成功返回
// 错值，hostpaths.UserHome 的 passwd 回落永远不会触发。LDAP / /opt/app
// 这类自定义 home 会静默用错目录，比不注入 HOME 更糟。
func fillServiceHomeDir(ctx context.Context, remote Remote, platformOS string, opts *ServiceOptions) {
	if opts == nil {
		return
	}
	if strings.TrimSpace(opts.HomeDir) != "" {
		return
	}
	if platformOS == "windows" {
		return
	}
	home, err := lookupRemoteUserHome(ctx, remote, opts.User)
	if err == nil && home != "" {
		opts.HomeDir = home
		return
	}
	fallback := opts.resolvedHomeDir(platformOS)
	log.Printf("[installer] WARN: 未能查到目标用户 home，回落到惯例路径 %s，若该用户 home 非惯例位置将导致 agent 解析到错误 home", fallback)
}

func lookupRemoteUserHome(ctx context.Context, remote Remote, user string) (string, error) {
	user = strings.TrimSpace(user)
	if user == "" {
		return "", fmt.Errorf("empty user")
	}

	out, err := remote.Run(ctx, getentPasswdCommand(user))
	if err == nil {
		if home, ok := passwdHomeField(out); ok {
			return home, nil
		}
		err = fmt.Errorf("getent passwd returned no home")
	}

	if !isSafeUnixUser(user) {
		return "", fmt.Errorf("getent failed: %w; skip eval echo ~user: unsafe username", err)
	}
	tildeOut, tildeErr := remote.Run(ctx, evalTildeHomeCommand(user))
	if tildeErr != nil {
		return "", fmt.Errorf("getent: %v; eval echo ~user: %w", err, tildeErr)
	}
	home := trimOutput(tildeOut)
	if !isAbsoluteUnixHome(home) {
		return "", fmt.Errorf("getent: %v; eval echo ~user returned %q", err, home)
	}
	return home, nil
}

// passwdHomeField 取 getent passwd / /etc/passwd 行的第 6 段（home）。
func passwdHomeField(raw string) (string, bool) {
	line := trimOutput(raw)
	if line == "" {
		return "", false
	}
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = trimOutput(line[:i])
	}
	parts := strings.Split(line, ":")
	if len(parts) < 6 {
		return "", false
	}
	home := strings.TrimSpace(parts[5])
	if !isAbsoluteUnixHome(home) {
		return "", false
	}
	return home, true
}

func isAbsoluteUnixHome(home string) bool {
	if home == "" || !strings.HasPrefix(home, "/") {
		return false
	}
	if strings.Contains(home, "\x00") || strings.ContainsAny(home, "\n\r") {
		return false
	}
	return true
}

func isSafeUnixUser(user string) bool {
	if user == "" || len(user) > 64 {
		return false
	}
	for i, r := range user {
		if r > unicode.MaxASCII {
			return false
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case i > 0 && (r >= '0' && r <= '9' || r == '-' || r == '.'):
		default:
			return false
		}
	}
	return true
}
