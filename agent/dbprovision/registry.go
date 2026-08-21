// registry.go —— 管理数据源登记与本地凭据文件的原子落盘。
//
// 职责：校验、探测、增删改查 PG/Redis 管理连接，并以 0600 权限持久化登记。
// 边界：不管理租约、不认识具体资源类型；活跃租约数由外部注入，资源供给由 Provisioner 完成。
package dbprovision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/xsxdot/gokit/logger"
)

// FileRegistry 是以 JSON 文件保存数据源登记的线程安全注册表。
type FileRegistry struct {
	mu           sync.RWMutex
	path         string
	items        map[string]DataSource
	leaseCounter func(string) int
}

// NewFileRegistry 创建一个文件注册表。
//
// 参数：path 是凭据文件路径；文件不存在时视为空注册表。
// 返回：初始化后的注册表或文件解析错误。
// 注意：已有文件解析失败会硬失败，避免把凭据损坏误认为登记为空。
func NewFileRegistry(path string) (*FileRegistry, error) {
	r := &FileRegistry{path: path, items: make(map[string]DataSource)}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取数据源注册表失败: %w", err)
	}
	var items []DataSource
	if err := json.Unmarshal(b, &items); err != nil {
		logger.GetLogger().WithEntryName("DBProvisionRegistry").WithField("path", path).WithErr(err).Error("解析数据源注册表失败")
		// 凭据文件解析失败不能静默当成空表，否则用户会误以为登记已丢失。
		return nil, fmt.Errorf("解析数据源注册表失败: %w", err)
	}
	for _, item := range items {
		if item.ID != "" {
			r.items[item.ID] = cloneDataSource(item)
		}
	}
	return r, nil
}

// SetActiveLeaseCounter 注入按数据源统计活跃租约的函数。
//
// 注意：注册表不反向依赖 store；调用方应在装配期注入，未注入时按零个活跃租约处理。
func (r *FileRegistry) SetActiveLeaseCounter(fn func(datasourceID string) int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.leaseCounter = fn
}

// Add 探测并新增一条数据源登记。
//
// 参数：ctx 控制探测；ds 是待登记的管理连接。
// 返回：带 ID、探测结果与创建时间的登记；探测失败不会落盘。
// 注意：Password 只保存在本地凭据文件和进程内存中，不会写日志。
func (r *FileRegistry) Add(ctx context.Context, ds DataSource) (DataSource, error) {
	if err := validateDataSource(ds); err != nil {
		return DataSource{}, err
	}
	p, ok := LookupProvisioner(ds.Kind)
	if !ok {
		return DataSource{}, fmt.Errorf("%w: %s", ErrUnsupportedKind, ds.Kind)
	}
	probe, err := p.Probe(ctx, ds)
	if err != nil {
		log := logger.GetLogger().WithEntryName("DBProvisionRegistry").WithField("kind", ds.Kind).WithField("name", ds.Name).WithField("missing", probe.Missing)
		log.WithErr(err).Error("数据源探测失败，拒绝登记")
		return DataSource{}, fmt.Errorf("数据源探测失败: %w", err)
	}
	if !probe.OK {
		err := fmt.Errorf("数据源探测未通过: %s; 修复提示: %s", probe.Error, probe.FixHint)
		logger.GetLogger().WithEntryName("DBProvisionRegistry").WithField("kind", ds.Kind).WithField("name", ds.Name).WithField("missing", probe.Missing).WithErr(err).Error("数据源探测失败，拒绝登记")
		return DataSource{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, item := range r.items {
		if item.Kind == ds.Kind && item.Name == ds.Name {
			return DataSource{}, fmt.Errorf("同 kind 下数据源名称已存在: %s", ds.Name)
		}
	}
	ds.ID = uuid.NewString()
	ds.Probe = probe
	ds.CreatedAt = time.Now()
	ds.Source = "manual"
	r.items[ds.ID] = cloneDataSource(ds)
	if err := r.saveLocked(); err != nil {
		delete(r.items, ds.ID)
		return DataSource{}, err
	}
	logger.GetLogger().WithEntryName("DBProvisionRegistry").WithFields(map[string]any{
		"id": ds.ID, "kind": ds.Kind, "name": ds.Name, "host": ds.Host, "port": ds.Port,
	}).Info("数据源登记成功")
	return cloneDataSource(ds), nil
}

// Update 探测并更新一条数据源登记。
//
// 参数：id 是原登记 ID；ds 是新配置。
// 返回：保留原 ID 与 CreatedAt 的更新后登记。
// 注意：Password 为空表示不修改旧密码，方便编辑表单只更新非敏感字段。
func (r *FileRegistry) Update(ctx context.Context, id string, ds DataSource) (DataSource, error) {
	r.mu.RLock()
	old, ok := r.items[id]
	r.mu.RUnlock()
	if !ok {
		return DataSource{}, ErrDataSourceNotFound
	}
	if err := validateDataSource(ds); err != nil {
		return DataSource{}, err
	}
	if ds.Password == "" {
		// 空串表示“不改密码”，否则每次编辑非敏感字段都必须重新输入秘密。
		ds.Password = old.Password
	}
	p, ok := LookupProvisioner(ds.Kind)
	if !ok {
		return DataSource{}, fmt.Errorf("%w: %s", ErrUnsupportedKind, ds.Kind)
	}
	probe, err := p.Probe(ctx, ds)
	if err != nil {
		return DataSource{}, fmt.Errorf("数据源探测失败: %w", err)
	}
	if !probe.OK {
		return DataSource{}, fmt.Errorf("数据源探测未通过: %s; 修复提示: %s", probe.Error, probe.FixHint)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for otherID, item := range r.items {
		if otherID != id && item.Kind == ds.Kind && item.Name == ds.Name {
			return DataSource{}, fmt.Errorf("同 kind 下数据源名称已存在: %s", ds.Name)
		}
	}
	ds.ID = old.ID
	ds.CreatedAt = old.CreatedAt
	ds.Source = old.Source
	ds.Probe = probe
	r.items[id] = cloneDataSource(ds)
	if err := r.saveLocked(); err != nil {
		return DataSource{}, err
	}
	logger.GetLogger().WithEntryName("DBProvisionRegistry").WithFields(map[string]any{
		"id": id, "kind": ds.Kind, "name": ds.Name, "password_changed": ds.Password != old.Password,
	}).Info("数据源更新成功")
	return cloneDataSource(ds), nil
}

// Remove 删除一条数据源登记。
//
// 参数：id 是登记 ID；force 为 true 时忽略活跃租约保护。
// 返回：删除错误；不存在的 ID 返回 ErrDataSourceNotFound。
// 注意：非强制删除不会影响已有租约，只会在有活跃租约时拒绝操作。
func (r *FileRegistry) Remove(_ context.Context, id string, force bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[id]; !ok {
		return ErrDataSourceNotFound
	}
	active := 0
	if r.leaseCounter != nil {
		active = r.leaseCounter(id)
	}
	if !force && active > 0 {
		logger.GetLogger().WithEntryName("DBProvisionRegistry").WithFields(map[string]any{"id": id, "active_leases": active}).Warn("活跃租约阻止删除数据源")
		return fmt.Errorf("%w: %d", ErrDataSourceInUse, active)
	}
	item := r.items[id]
	delete(r.items, id)
	if err := r.saveLocked(); err != nil {
		r.items[id] = item
		return err
	}
	logger.GetLogger().WithEntryName("DBProvisionRegistry").WithFields(map[string]any{"id": id, "forced": force}).Info("数据源删除成功")
	return nil
}

// Get 按 ID 读取一条数据源登记。
func (r *FileRegistry) Get(_ context.Context, id string) (DataSource, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.items[id]
	if !ok {
		return DataSource{}, ErrDataSourceNotFound
	}
	return cloneDataSource(item), nil
}

// GetByName 按 kind 与展示名读取一条数据源登记。
func (r *FileRegistry) GetByName(_ context.Context, kind, name string) (DataSource, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, item := range r.items {
		if item.Kind == kind && item.Name == name {
			return cloneDataSource(item), nil
		}
	}
	return DataSource{}, ErrDataSourceNotFound
}

// List 返回全部数据源登记的副本。
//
// 注意：该方法保留密码供内部调用；对外响应必须逐条调用 Sanitized。
func (r *FileRegistry) List(_ context.Context) ([]DataSource, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]DataSource, 0, len(r.items))
	for _, item := range r.items {
		items = append(items, cloneDataSource(item))
	}
	return items, nil
}

// Probe 重新探测指定数据源并持久化最新结果。
//
// 返回：探测结论；探测流程错误或数据源不存在时返回 error。
func (r *FileRegistry) Probe(ctx context.Context, id string) (ProbeResult, error) {
	r.mu.RLock()
	ds, ok := r.items[id]
	r.mu.RUnlock()
	if !ok {
		return ProbeResult{}, ErrDataSourceNotFound
	}
	p, ok := LookupProvisioner(ds.Kind)
	if !ok {
		return ProbeResult{}, fmt.Errorf("%w: %s", ErrUnsupportedKind, ds.Kind)
	}
	probe, err := p.Probe(ctx, ds)
	if err != nil {
		return probe, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.items[id]
	if !ok {
		return ProbeResult{}, ErrDataSourceNotFound
	}
	current.Probe = probe
	r.items[id] = current
	if err := r.saveLocked(); err != nil {
		return ProbeResult{}, err
	}
	return probe, nil
}

func validateDataSource(ds DataSource) error {
	if strings.TrimSpace(ds.Kind) == "" || strings.TrimSpace(ds.Name) == "" || strings.TrimSpace(ds.Host) == "" {
		return errors.New("数据源 kind、name、host 不能为空")
	}
	if ds.Port < 1 || ds.Port > 65535 {
		return fmt.Errorf("数据源端口必须在 1..65535 内: %d", ds.Port)
	}
	return nil
}

func (r *FileRegistry) saveLocked() error {
	items := make([]DataSource, 0, len(r.items))
	for _, item := range r.items {
		items = append(items, item)
	}
	b, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化数据源注册表失败: %w", err)
	}
	if dir := filepath.Dir(r.path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("创建数据源注册表目录失败: %w", err)
		}
	}
	tmpPath := r.path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		logger.GetLogger().WithEntryName("DBProvisionRegistry").WithFields(map[string]any{"path": r.path}).WithErr(err).Error("打开数据源注册表临时文件失败")
		return fmt.Errorf("打开数据源注册表临时文件失败: %w", err)
	}
	writeErr := func() error {
		if _, err := f.Write(b); err != nil {
			return err
		}
		if err := f.Sync(); err != nil {
			return err
		}
		return f.Close()
	}()
	if writeErr != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		logger.GetLogger().WithEntryName("DBProvisionRegistry").WithFields(map[string]any{"path": r.path}).WithErr(writeErr).Error("落盘数据源注册表失败")
		return fmt.Errorf("落盘数据源注册表失败: %w", writeErr)
	}
	if err := os.Rename(tmpPath, r.path); err != nil {
		_ = os.Remove(tmpPath)
		logger.GetLogger().WithEntryName("DBProvisionRegistry").WithFields(map[string]any{"path": r.path}).WithErr(err).Error("原子替换数据源注册表失败")
		return fmt.Errorf("原子替换数据源注册表失败: %w", err)
	}
	if err := os.Chmod(r.path, 0o600); err != nil {
		logger.GetLogger().WithEntryName("DBProvisionRegistry").WithFields(map[string]any{"path": r.path}).WithErr(err).Error("修正数据源注册表权限失败")
		return fmt.Errorf("修正数据源注册表权限失败: %w", err)
	}
	return nil
}

func cloneDataSource(ds DataSource) DataSource {
	clone := ds
	if ds.Extra != nil {
		clone.Extra = make(map[string]string, len(ds.Extra))
		for k, v := range ds.Extra {
			clone.Extra[k] = v
		}
	}
	clone.Probe.Capabilities = cloneBoolMap(ds.Probe.Capabilities)
	clone.Probe.Facts = cloneStringMap(ds.Probe.Facts)
	clone.Probe.Missing = append([]string(nil), ds.Probe.Missing...)
	return clone
}

func cloneBoolMap(src map[string]bool) map[string]bool {
	if src == nil {
		return nil
	}
	dst := make(map[string]bool, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
