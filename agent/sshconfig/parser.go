// Package sshconfig 解析 ~/.ssh/config 的子集字段。
//
// 职责：
//   - 提取 Host / HostName / Port / User / IdentityFile
//   - 跳过通配符条目(Host 含 * 或 ?)
//   - Port 缺省 22
//
// 边界：
//   - 不支持 Include、Match、ProxyCommand 等高级指令
//   - 不展开 ~ 或环境变量;原样返回给调用方
package sshconfig

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Host 是从 ssh config 中解析出的单条主机记录。
type Host struct {
	Name         string `json:"name"`
	HostName     string `json:"host_name"`
	Port         int    `json:"port"`
	User         string `json:"user"`
	IdentityFile string `json:"identity_file"`
}

// DefaultPath 返回 ~/.ssh/config 的绝对路径。
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

// ParseFile 读取并解析指定路径的 ssh config。
//
// 文件不存在时返回空切片而非错误,方便首次使用场景。
func ParseFile(path string) ([]Host, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return []Host{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Parse 从任意 io.Reader 解析 ssh config 内容。
func Parse(r io.Reader) ([]Host, error) {
	scanner := bufio.NewScanner(r)
	var hosts []Host
	var current *Host
	flush := func() {
		if current != nil && !strings.ContainsAny(current.Name, "*?") {
			if current.Port == 0 {
				current.Port = 22
			}
			hosts = append(hosts, *current)
		}
		current = nil
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value := splitKV(line)
		if key == "" {
			continue
		}
		lower := strings.ToLower(key)
		if lower == "host" {
			flush()
			current = &Host{Name: value}
			continue
		}
		if current == nil {
			continue
		}
		switch lower {
		case "hostname":
			current.HostName = value
		case "port":
			if p, err := strconv.Atoi(value); err == nil {
				current.Port = p
			}
		case "user":
			current.User = value
		case "identityfile":
			current.IdentityFile = value
		}
	}
	flush()
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return hosts, nil
}

// splitKV 按第一段连续空白切分行,返回 (key, value)。
//
// SSH config 支持 "Host foo" 和 "Host=foo" 两种,这里仅处理空白。
func splitKV(line string) (string, string) {
	idx := strings.IndexFunc(line, func(r rune) bool { return r == ' ' || r == '\t' })
	if idx < 0 {
		return line, ""
	}
	key := line[:idx]
	value := strings.TrimSpace(line[idx:])
	return key, value
}
