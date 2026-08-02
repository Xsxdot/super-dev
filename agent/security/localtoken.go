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

// RotateLocalToken 生成新 token 并原子替换 <dataDir>/local-access-token（0600）。
//
// 参数：
//   - dataDir: agent 数据目录（调用方保证已存在，main.go 启动时已 MkdirAll）
//
// 返回：
//   - 新生成的 token（64 位 hex）
//   - 写入/替换失败时的错误
//
// 注意：
//   - 每次调用都生成新值——「agent 启动」是唯一轮换触发点，旧 token 立即失效；
//     客户端靠 401 后重读文件感知轮换，无需通知通道。
//   - 写入走「0600 临时文件 + rename 原子替换」而非直接 WriteFile：覆盖写会继承
//     旧文件的历史权限（可能是宽的 0644），在 Chmod 收紧前留下新 token 可被其他
//     本地用户读到的窗口；rename 同时保证并发读方只会读到完整的旧值或新值。
func RotateLocalToken(dataDir string) (string, error) {
	token, err := GenerateToken()
	if err != nil {
		return "", fmt.Errorf("generate local access token: %w", err)
	}
	path := LocalTokenPath(dataDir)
	// CreateTemp 建出的文件天生 0600：新 token 从出生起就只属主可读。
	tmp, err := os.CreateTemp(dataDir, LocalTokenFileName+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create temp local access token in %s: %w", dataDir, err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("write local access token %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("close local access token %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("replace local access token %s: %w", path, err)
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
