// Package identity 管理本机节点身份。
//
// 职责：
//   - 首次启动时随机生成 node_id（格式：superdev-xxxx），写入 dataDir/identity.json
//   - 后续启动时加载已有 node_id，保证稳定不变
//   - 提供 Save 供设置页更新 display_name
//
// 边界：
//   - dataDir 由调用方（api.App）传入，通常为 ~/.superdev
//   - 不暴露文件路径常量，路径拼接在包内完成
package identity

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const fileName = "identity.json"

// Identity 表示本机节点身份。
type Identity struct {
	// NodeID 是本机的稳定唯一标识，格式为 "superdev-xxxx"（4 位十六进制）。
	// 首次生成后不可更改。
	NodeID string `json:"node_id"`
	// DisplayName 是用户可自定义的显示名称，默认为机器名。
	DisplayName string `json:"display_name"`
}

// LoadOrCreate 从 dataDir/identity.json 加载身份文件。
// 文件不存在时生成新 node_id 并写入。
//
// 参数：
//   - dataDir: 数据目录，通常为 ~/.superdev
//
// 返回：
//   - 已加载或新生成的 Identity
//   - 读取或写入失败时返回 error
func LoadOrCreate(dataDir string) (Identity, error) {
	path := filepath.Join(dataDir, fileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return createAndSave(dataDir)
	}
	if err != nil {
		return Identity{}, fmt.Errorf("read identity: %w", err)
	}
	var id Identity
	if err := json.Unmarshal(data, &id); err != nil {
		return Identity{}, fmt.Errorf("parse identity: %w", err)
	}
	return id, nil
}

// Save 将 Identity 写回 dataDir/identity.json。
//
// 参数：
//   - dataDir: 数据目录
//   - id: 要保存的身份信息
//
// 注意：
//   - 通常只用于更新 DisplayName；NodeID 字段不做校验，调用方负责不修改它
func Save(dataDir string, id Identity) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("mkdir identity dir: %w", err)
	}
	data, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal identity: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dataDir, fileName), data, 0o600)
}

func createAndSave(dataDir string) (Identity, error) {
	nodeID, err := generateNodeID()
	if err != nil {
		return Identity{}, fmt.Errorf("generate node_id: %w", err)
	}
	displayName, err := os.Hostname()
	if err != nil || displayName == "" {
		displayName = nodeID
	}
	id := Identity{NodeID: nodeID, DisplayName: displayName}
	if err := Save(dataDir, id); err != nil {
		return Identity{}, err
	}
	return id, nil
}

// generateNodeID 生成 "superdev-xxxx" 格式的随机 node_id（2 字节 = 4 位十六进制）。
func generateNodeID() (string, error) {
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "superdev-" + hex.EncodeToString(b), nil
}
