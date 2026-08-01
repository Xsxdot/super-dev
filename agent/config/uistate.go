// uistate.go —— agent 数据目录下的项目 UI 偏好存储。
//
// 职责：
//   - 持久化纯 UI 状态（如各环境勾选的服务列表）到 dataDir/uistate.json
//   - 以项目 rootPath 为键隔离各项目
//
// 边界：
//   - 只存 UI 偏好，不存任何项目配置/运行态
//   - split 格式项目的 env_selected_service_ids 唯一归宿；legacy 项目
//     迁移前仍走 config.yaml（loader 负责），本 store 不参与
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// UIStateStore 读写 uistate.json，方法并发安全。
type UIStateStore struct {
	mu   sync.Mutex
	path string
}

// projectUIState 是单个项目的 UI 偏好集合。
type projectUIState struct {
	EnvSelectedServiceIDs map[string][]string `json:"env_selected_service_ids,omitempty"`
}

// NewUIStateStore 创建使用 dataDir/uistate.json 的存储。
func NewUIStateStore(dataDir string) *UIStateStore {
	return &UIStateStore{path: filepath.Join(dataDir, "uistate.json")}
}

// EnvSelected 返回项目各环境勾选的服务列表；无记录返回 nil。
func (s *UIStateStore) EnvSelected(rootPath string) map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, _ := s.load()
	return all[rootPath].EnvSelectedServiceIDs
}

// SetEnvSelected 更新项目单个环境的勾选列表并持久化。
func (s *UIStateStore) SetEnvSelected(rootPath, envName string, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.load()
	if err != nil {
		return err
	}
	st := all[rootPath]
	if st.EnvSelectedServiceIDs == nil {
		st.EnvSelectedServiceIDs = map[string][]string{}
	}
	st.EnvSelectedServiceIDs[envName] = ids
	all[rootPath] = st
	return s.save(all)
}

// ReplaceEnvSelected 整体替换项目的勾选状态（迁移 apply 一次性搬入时使用）。
func (s *UIStateStore) ReplaceEnvSelected(rootPath string, sel map[string][]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.load()
	if err != nil {
		return err
	}
	st := all[rootPath]
	st.EnvSelectedServiceIDs = sel
	all[rootPath] = st
	return s.save(all)
}

// load 读取整个 uistate 文件；不存在返回空 map。调用方必须已持锁。
func (s *UIStateStore) load() (map[string]projectUIState, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]projectUIState{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read uistate: %w", err)
	}
	out := map[string]projectUIState{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse uistate: %w", err)
	}
	return out, nil
}

// save 写回整个 uistate 文件。调用方必须已持锁。
func (s *UIStateStore) save(all map[string]projectUIState) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir uistate dir: %w", err)
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal uistate: %w", err)
	}
	return os.WriteFile(s.path, append(data, '\n'), 0o644)
}
