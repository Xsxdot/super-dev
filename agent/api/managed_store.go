// managed_store.go 持久化远端 agent 的 managed deployment 声明式清单。
//
// 职责：
//   - 在 DataDir/managed-deployments.json 读写 []model.ManagedDeployment
//   - 使用 temp + rename 原子替换，避免进程崩溃留下半截 JSON
//
// 边界：
//   - 不执行 collector reconcile
//   - 不修改 App 内存项目列表
package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/xsxdot/super-dev/agent/model"
)

// ManagedStore 管理 managed-deployments.json 的读写。
type ManagedStore struct {
	mu   sync.Mutex
	path string
}

// NewManagedStore 创建 ManagedStore。
//
// 参数：
//   - dataDir: agent 数据目录
//
// 返回：
//   - 指向 dataDir/managed-deployments.json 的 store
func NewManagedStore(dataDir string) *ManagedStore {
	return &ManagedStore{path: filepath.Join(dataDir, "managed-deployments.json")}
}

// Load 读取 managed deployment 清单。
//
// 返回：
//   - 文件不存在时返回空切片
//   - JSON 损坏或读文件失败时返回错误
func (s *ManagedStore) Load() ([]model.ManagedDeployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return []model.ManagedDeployment{}, nil
	}
	if err != nil {
		return nil, err
	}

	var list []model.ManagedDeployment
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []model.ManagedDeployment{}
	}
	return list, nil
}

// Save 原子写入 managed deployment 清单。
//
// 参数：
//   - list: 要完整覆盖的期望清单，nil 会保存为空数组
//
// 返回：
//   - 目录创建、序列化、写入或 rename 失败时返回错误
func (s *ManagedStore) Save(list []model.ManagedDeployment) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if list == nil {
		list = []model.ManagedDeployment{}
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
