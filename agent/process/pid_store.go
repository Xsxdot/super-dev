// pid_store.go 持久化 deploymentID → PID 映射，用于 superdev 重启时清理孤儿进程。
//
// 职责：
//   - 内存中维护映射，Flush 写入 JSON 文件
//   - KillAll 读文件、kill 所有存活进程、清空文件
//
// 边界：
//   - 仅处理 local 进程的 PID；remote deployment 无需记录
//   - 不感知 deployment 状态，仅做 pid→kill 的原子操作
package process

import (
	"encoding/json"
	"os"
	"sync"
	"syscall"
)

// PIDStore 持久化 deploymentID → PID 映射，用于 superdev 重启时清理孤儿进程。
type PIDStore struct {
	mu   sync.Mutex
	path string
	pids map[string]int
}

// NewPIDStore 创建 PIDStore，path 为 JSON 文件路径（如 ~/.superdev/pids.json）。
func NewPIDStore(path string) *PIDStore {
	return &PIDStore{path: path, pids: map[string]int{}}
}

// Set 在内存中记录 deploymentID 对应的 PID。需调用 Flush 持久化。
func (s *PIDStore) Set(deploymentID string, pid int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pids[deploymentID] = pid
}

// Remove 从内存中删除 deploymentID 的 PID 记录。需调用 Flush 持久化。
func (s *PIDStore) Remove(deploymentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pids, deploymentID)
}

// Flush 将当前内存映射写入 JSON 文件。
func (s *PIDStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(s.pids)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

// LoadAll 从 JSON 文件加载所有 PID 记录，文件不存在时返回空 map。
func (s *PIDStore) LoadAll() map[string]int {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return map[string]int{}
	}
	var pids map[string]int
	if err := json.Unmarshal(data, &pids); err != nil {
		return map[string]int{}
	}
	return pids
}

// KillAll 读取 pid 文件，对每个存活的进程发送 SIGKILL（整个进程组），然后清空文件。
//
// 在 superdev 启动时调用，确保上次运行遗留的子进程全部终止。
func (s *PIDStore) KillAll() {
	pids := s.LoadAll()
	for _, pid := range pids {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	_ = os.WriteFile(s.path, []byte("{}"), 0o600)
	s.mu.Lock()
	s.pids = map[string]int{}
	s.mu.Unlock()
}
