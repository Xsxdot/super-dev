// Package projecthome 持久化「项目 → 归属主机」的本地路由标记。
//
// 职责：
//   - 把每个项目当前归属的主机 ID 与归属机侧项目目录保存到
//     <DataDir>/project-homes.json
//   - 提供归属查询（HomeOf/RemoteDirOf）、归属切换（SetHome）、按主机反查
//     （ProjectsHomedOn）
//
// 边界：
//   - 不校验 hostID 是否真实存在于 remote.Store——归属和主机生命周期是两层
//     职责，主机被删除后归属条目仍保留，由调用方（listProjects DTO 组装、
//     主机删除守卫）决定如何优雅降级或拦截
//   - 不下发到节点，也不写入 project.yaml：归属描述的是"这台控制面自己怎么
//     消费这个项目"（本地路由决策），随 project.yaml 走 git 流动会把 A 机的
//     归属强加给 B 机——同一份 project.yaml 在两台控制面上完全可能有不同归属
//   - 文件形态 {"proj-id": {"host_id": "...", "remote_dir": "..."}}；兼容读取
//     早期的 {"proj-id": "host-id"} 扁平格式（下次写入即升级为对象格式）；
//     某项目缺席该 map 即视为归属本机
package projecthome

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Entry 是单个项目的归属记录。
type Entry struct {
	HostID string `json:"host_id"`
	// RemoteDir 是转移时在归属机上实际使用的项目绝对路径（switch_home 时刻
	// 由转移引擎写入）。迁回时优先用它定位归属机仓库——自定义目录 + agent
	// 重启后，内存中的转移记录已丢失，没有这条持久化就只能回退到默认目录
	// 猜测，必然打错。可为空（早期记录/异常路径），空时由调用方回退默认值。
	RemoteDir string `json:"remote_dir,omitempty"`
}

// Store 持久化项目归属映射。
//
// 线程安全：所有方法持有 mu，遵循「读取整份文件→内存修改→原子写回」惯例
// （文件读写模式对齐 remote.Store），写入落盘时走临时文件 + rename 原子替换，
// 避免进程崩溃或并发写入截断出半份 JSON。
type Store struct {
	mu   sync.Mutex
	path string
}

// NewStore 创建归属 Store，path 是 <DataDir>/project-homes.json 的完整路径。
//
// 参数：
//   - path: 归属映射 JSON 文件完整路径，文件不存在时视为空映射，不报错
//
// 返回：
//   - 归属 Store
//   - 文件存在但内容损坏（无法解析为 JSON）时返回错误——启动即失败，
//     好过带着一份读不出来的归属状态静默把所有项目当成本机处理
func NewStore(path string) (*Store, error) {
	s := &Store{path: path}
	if _, err := s.load(); err != nil {
		return nil, fmt.Errorf("加载项目归属文件 %s 失败: %w", path, err)
	}
	return s, nil
}

// HomeOf 返回项目当前归属的主机 ID；"" 表示归属本机（默认态）。
func (s *Store) HomeOf(projectID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		// 查询路径不阻断：加载失败保守按本机处理，错误已在 NewStore 阶段
		// 拦截过一次，这里出现多半是运行期文件被外部破坏，打日志留痕即可。
		log.Printf("[SuperDev] projecthome: 加载归属文件失败，按本机处理 project=%s err=%v", projectID, err)
		return ""
	}
	return m[projectID].HostID
}

// RemoteDirOf 返回项目转移时记录的归属机侧项目目录；未记录（早期条目/项目
// 归属本机）返回 ""，由调用方决定回退策略。
func (s *Store) RemoteDirOf(projectID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		log.Printf("[SuperDev] projecthome: 加载归属文件失败，remote_dir 按空处理 project=%s err=%v", projectID, err)
		return ""
	}
	return m[projectID].RemoteDir
}

// SetHome 设置项目归属主机；hostID=="" 表示迁回本机（删除映射条目）。
// remoteDir 是归属机侧项目绝对路径，随归属一并记录（迁回时定位仓库用），
// 允许为空（调用方无法确定路径时保守留空，迁回回退默认目录）。
//
// 归属切换是路由行为的分水岭——后续所有针对该项目的操作（启停、日志、部署）
// 都会依据这个标记决定在本机执行还是转发到目标主机，因此必须留下可追溯的日志。
func (s *Store) SetHome(projectID, hostID, remoteDir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	old := m[projectID].HostID
	if hostID == "" {
		delete(m, projectID)
	} else {
		m[projectID] = Entry{HostID: hostID, RemoteDir: remoteDir}
	}
	if err := s.save(m); err != nil {
		return err
	}
	log.Printf("[SuperDev] projecthome: 项目 %s 归属 %s→%s remote_dir=%s", projectID, old, hostID, remoteDir)
	return nil
}

// ProjectsHomedOn 返回所有归属指定主机的项目 ID，按字典序排列。
//
// 供主机删除守卫使用：删除一台主机前先查有没有项目归属于它，避免留下
// 指向已消失主机的悬空归属。
func (s *Store) ProjectsHomedOn(hostID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		log.Printf("[SuperDev] projecthome: 加载归属文件失败，按空列表处理 host=%s err=%v", hostID, err)
		return nil
	}
	var ids []string
	for projectID, entry := range m {
		if entry.HostID == hostID {
			ids = append(ids, projectID)
		}
	}
	sort.Strings(ids)
	return ids
}

// load 读取整份归属映射；文件不存在或为空时返回空映射，不报错。
//
// 双格式兼容：优先按对象格式 {"id": {"host_id": ...}} 解析；失败则尝试早期
// 扁平格式 {"id": "host-id"}（切面 4 初版落盘的形态）并就地转换——不强制
// 一次性迁移文件，下次 SetHome 写回时自然升级为对象格式。
func (s *Store) load() (map[string]Entry, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return map[string]Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return map[string]Entry{}, nil
	}
	var m map[string]Entry
	if err := json.Unmarshal(data, &m); err == nil {
		if m == nil {
			m = map[string]Entry{}
		}
		return m, nil
	}
	var legacy map[string]string
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, err
	}
	m = make(map[string]Entry, len(legacy))
	for projectID, hostID := range legacy {
		m[projectID] = Entry{HostID: hostID}
	}
	return m, nil
}

// save 把整份归属映射原子写回磁盘（临时文件 + rename）。
func (s *Store) save(m map[string]Entry) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}
