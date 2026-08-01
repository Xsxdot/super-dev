// localtoken.go —— 本机访问 token（local-access-token）的生成、轮换与读取。
//
// 职责：
//   - 在 agent 数据目录维护仅属主可读（0600）的 local-access-token 文件
//   - 提供「启动即轮换」原语：每次 agent 启动生成新值，旧值随之失效
//   - 供同机同用户客户端（桌面端、superdev-mcp、CLI）读取完成鉴权
//
// 边界：
//   - 不落 security.json：本机 token 只存于该独立文件与进程内存（Store.localToken）
//   - 不做跨用户授权：文件系统权限即信任边界（docker socket / kubeconfig 同款模型）
//   - 校验在 Store.VerifyLocalToken，本文件只管文件形态
package security

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// LocalTokenFileName 是数据目录下本机访问 token 的文件名。
const LocalTokenFileName = "local-access-token"

// LocalTokenPath 返回 dataDir 下 local-access-token 的完整路径。
func LocalTokenPath(dataDir string) string {
	return filepath.Join(dataDir, LocalTokenFileName)
}

// RotateLocalToken 生成新 token 并覆盖写入 <dataDir>/local-access-token（0600）。
//
// 参数：
//   - dataDir: agent 数据目录（调用方保证已存在，main.go 启动时已 MkdirAll）
//
// 返回：
//   - 新生成的 token（64 位 hex）
//   - 写入/chmod 失败时的错误
//
// 注意：
//   - 每次调用都生成新值——「agent 启动」是唯一轮换触发点，旧 token 立即失效；
//     客户端靠 401 后重读文件感知轮换，无需通知通道。
//   - os.WriteFile 对已存在文件不改权限，故显式 Chmod 收紧历史宽权限。
func RotateLocalToken(dataDir string) (string, error) {
	token, err := GenerateToken()
	if err != nil {
		return "", fmt.Errorf("generate local access token: %w", err)
	}
	path := LocalTokenPath(dataDir)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write local access token %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("chmod local access token %s: %w", path, err)
	}
	// 只打路径不打值：token 是凭据，绝不落日志。
	log.Printf("[SuperDev] security: local access token rotated at %s", path)
	return token, nil
}

// ReadLocalToken 读取 <dataDir>/local-access-token 并去除首尾空白。
//
// 返回：
//   - token 内容；文件缺失/不可读时返回原始错误（调用方决定呈现指引）
func ReadLocalToken(dataDir string) (string, error) {
	data, err := os.ReadFile(LocalTokenPath(dataDir))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
