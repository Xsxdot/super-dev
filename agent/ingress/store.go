// store.go 持久化 Ingress 声明、落地状态和 provider 配置。
//
// 职责：
//   - 在 agent DataDir 下读写 ingress JSON 文件
//   - 为声明分配稳定 ID
//   - 列表接口隐藏 DNS provider 密文字段
//
// 边界：
//   - 不校验 provider 凭据是否可用
//   - 不执行 DNS、证书或远端主机操作
package ingress

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Store 定义入口声明、落地状态和 DNS provider 配置的持久化能力。
type Store interface {
	// ListIngress 返回所有入口声明。
	ListIngress() ([]Ingress, error)
	// ListIngressByProject 返回指定项目下的入口声明。
	ListIngressByProject(projectID string) ([]Ingress, error)
	// GetIngress 按 ID 读取入口声明。
	GetIngress(id string) (Ingress, bool, error)
	// UpsertIngress 新增或覆盖入口声明。
	UpsertIngress(in Ingress) (Ingress, error)
	// DeleteIngress 删除入口声明但不删除远端资源。
	DeleteIngress(id string) error
	// SaveState 保存入口落地状态。
	SaveState(state AppliedState) error
	// GetState 按入口 ID 读取落地状态。
	GetState(ingressID string) (AppliedState, bool, error)
	// ListStates 返回所有入口落地状态。
	ListStates() ([]AppliedState, error)
	// ListDNSProviders 返回已脱敏的 DNS provider 列表。
	ListDNSProviders() ([]DNSProviderConfig, error)
	// GetDNSProvider 读取包含 secrets 的 DNS provider 完整配置。
	GetDNSProvider(id string) (DNSProviderConfig, bool, error)
	// UpsertDNSProvider 新增或覆盖 DNS provider 配置。
	UpsertDNSProvider(cfg DNSProviderConfig) (DNSProviderConfig, error)
	// DeleteDNSProvider 删除 DNS provider 配置。
	DeleteDNSProvider(id string) error
	// ListCertificates 返回托管证书列表，并隐藏 Material.KeyPEM。
	ListCertificates() ([]ManagedCertificate, error)
	// GetCertificate 读取包含完整 PEM 的托管证书，供内部申请、续期和部署使用。
	GetCertificate(id string) (ManagedCertificate, bool, error)
	// UpsertCertificate 新增或覆盖托管证书。
	UpsertCertificate(cert ManagedCertificate) (ManagedCertificate, error)
	// DeleteCertificate 删除托管证书。
	DeleteCertificate(id string) error
	// GetACMEAccount 读取全局 ACME 账号。
	GetACMEAccount() (ACMEAccount, error)
	// SaveACMEAccount 保存全局 ACME 账号。
	SaveACMEAccount(acc ACMEAccount) error
}

// DNSProviderConfig 描述一个 DNS provider 实例及其本机保存的密文配置。
type DNSProviderConfig struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	ZoneID  string            `json:"zone_id,omitempty"`
	Secrets map[string]string `json:"secrets,omitempty"`
}

type fileStoreData struct {
	Ingresses []Ingress      `json:"ingresses"`
	States    []AppliedState `json:"states"`
}

type providerStoreData struct {
	DNSProviders []DNSProviderConfig `json:"dns_providers"`
}

type certStoreData struct {
	Account      ACMEAccount          `json:"account"`
	Certificates []ManagedCertificate `json:"certificates"`
}

// FileStore 使用 DataDir 下的 JSON 文件保存入口配置子系统状态。
type FileStore struct {
	mu            sync.Mutex
	ingressPath   string
	providersPath string
	certsPath     string
}

// NewFileStore 创建一个 JSON 文件存储。
//
// 参数：
//   - dataDir: agent 数据目录
//
// 返回：
//   - 可读写 ingress.json、ingress-providers.json 和 ingress-certs.json 的 FileStore
func NewFileStore(dataDir string) *FileStore {
	return &FileStore{
		ingressPath:   filepath.Join(dataDir, "ingress.json"),
		providersPath: filepath.Join(dataDir, "ingress-providers.json"),
		certsPath:     filepath.Join(dataDir, "ingress-certs.json"),
	}
}

// ListIngress 返回所有入口声明。
//
// 返回：
//   - 入口声明列表，文件不存在时为空列表
//   - 读取或解析失败时返回错误
func (s *FileStore) ListIngress() ([]Ingress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadData()
	if err != nil {
		return nil, err
	}
	return append([]Ingress(nil), data.Ingresses...), nil
}

// ListIngressByProject 返回指定项目下的入口声明。
//
// 参数：
//   - projectID: 项目 ID
//
// 返回：
//   - 只属于该项目的入口声明列表
//   - 读取或解析失败时返回错误
func (s *FileStore) ListIngressByProject(projectID string) ([]Ingress, error) {
	items, err := s.ListIngress()
	if err != nil {
		return nil, err
	}
	out := make([]Ingress, 0, len(items))
	for _, item := range items {
		if item.ProjectID == projectID {
			out = append(out, item)
		}
	}
	return out, nil
}

// GetIngress 按 ID 读取入口声明。
//
// 参数：
//   - id: 入口声明 ID
//
// 返回：
//   - 命中的 Ingress
//   - 是否存在
//   - 读取或解析失败时返回错误
func (s *FileStore) GetIngress(id string) (Ingress, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadData()
	if err != nil {
		return Ingress{}, false, err
	}
	for _, in := range data.Ingresses {
		if in.ID == id {
			return in, true, nil
		}
	}
	return Ingress{}, false, nil
}

// UpsertIngress 新增或覆盖入口声明。
//
// 参数：
//   - in: 待保存的入口声明，ID 为空时会自动分配
//
// 返回：
//   - 保存后的入口声明
//   - 读写失败时返回错误
func (s *FileStore) UpsertIngress(in Ingress) (Ingress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadData()
	if err != nil {
		return Ingress{}, err
	}
	now := time.Now().UTC()
	if in.ID == "" {
		in.ID = uuid.NewString()
		in.CreatedAt = now
	}
	in.UpdatedAt = now
	for i, existing := range data.Ingresses {
		if existing.ID == in.ID {
			if in.CreatedAt.IsZero() {
				in.CreatedAt = existing.CreatedAt
			}
			data.Ingresses[i] = in
			return in, s.saveData(data)
		}
	}
	data.Ingresses = append(data.Ingresses, in)
	return in, s.saveData(data)
}

// DeleteIngress 删除指定入口声明。
//
// 参数：
//   - id: 入口声明 ID
//
// 返回：
//   - 写入失败时返回错误
//
// 注意：
//   - 删除声明不会自动删除生产入口资源，远端资源删除必须走孤儿资源确认流程
func (s *FileStore) DeleteIngress(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadData()
	if err != nil {
		return err
	}
	filtered := data.Ingresses[:0]
	for _, in := range data.Ingresses {
		if in.ID != id {
			filtered = append(filtered, in)
		}
	}
	data.Ingresses = filtered
	return s.saveData(data)
}

// SaveState 保存入口落地状态。
//
// 参数：
//   - state: 待保存的落地状态，以 IngressID 为唯一键
//
// 返回：
//   - 读写失败时返回错误
func (s *FileStore) SaveState(state AppliedState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadData()
	if err != nil {
		return err
	}
	state.UpdatedAt = time.Now().UTC()
	for i, existing := range data.States {
		if existing.IngressID == state.IngressID {
			data.States[i] = state
			return s.saveData(data)
		}
	}
	data.States = append(data.States, state)
	return s.saveData(data)
}

// GetState 按入口 ID 读取落地状态。
//
// 参数：
//   - ingressID: 入口声明 ID
//
// 返回：
//   - 命中的落地状态
//   - 是否存在
//   - 读取或解析失败时返回错误
func (s *FileStore) GetState(ingressID string) (AppliedState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadData()
	if err != nil {
		return AppliedState{}, false, err
	}
	for _, state := range data.States {
		if state.IngressID == ingressID {
			return state, true, nil
		}
	}
	return AppliedState{}, false, nil
}

// ListStates 返回所有入口落地状态。
//
// 返回：
//   - 落地状态列表，文件不存在时为空列表
//   - 读取或解析失败时返回错误
func (s *FileStore) ListStates() ([]AppliedState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadData()
	if err != nil {
		return nil, err
	}
	return append([]AppliedState(nil), data.States...), nil
}

// ListDNSProviders 返回所有 DNS provider 元数据，并隐藏密文字段。
//
// 返回：
//   - 脱敏后的 DNS provider 配置列表
//   - 读取或解析失败时返回错误
func (s *FileStore) ListDNSProviders() ([]DNSProviderConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadProviderData()
	if err != nil {
		return nil, err
	}
	out := append([]DNSProviderConfig(nil), data.DNSProviders...)
	for i := range out {
		out[i].Secrets = nil
	}
	return out, nil
}

// GetDNSProvider 按 ID 读取 DNS provider 完整配置。
//
// 参数：
//   - id: DNS provider ID
//
// 返回：
//   - 命中的 DNS provider 配置，包含密文字段
//   - 是否存在
//   - 读取或解析失败时返回错误
func (s *FileStore) GetDNSProvider(id string) (DNSProviderConfig, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadProviderData()
	if err != nil {
		return DNSProviderConfig{}, false, err
	}
	for _, cfg := range data.DNSProviders {
		if cfg.ID == id {
			return cfg, true, nil
		}
	}
	return DNSProviderConfig{}, false, nil
}

// UpsertDNSProvider 新增或覆盖 DNS provider 配置。
//
// 参数：
//   - cfg: DNS provider 配置，ID 为空时会自动分配
//
// 返回：
//   - 保存后的完整配置
//   - 读写失败时返回错误
//
// 注意：
//   - 更新已有 provider 且未传 secrets 时，会保留旧 secrets
func (s *FileStore) UpsertDNSProvider(cfg DNSProviderConfig) (DNSProviderConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cfg.ID == "" {
		cfg.ID = uuid.NewString()
	}
	data, err := s.loadProviderData()
	if err != nil {
		return DNSProviderConfig{}, err
	}
	for i, existing := range data.DNSProviders {
		if existing.ID == cfg.ID {
			if len(cfg.Secrets) == 0 {
				cfg.Secrets = existing.Secrets
			}
			data.DNSProviders[i] = cfg
			return cfg, s.saveProviderData(data)
		}
	}
	data.DNSProviders = append(data.DNSProviders, cfg)
	return cfg, s.saveProviderData(data)
}

// DeleteDNSProvider 删除 DNS provider 配置。
//
// 参数：
//   - id: DNS provider ID
//
// 返回：
//   - 写入失败时返回错误
func (s *FileStore) DeleteDNSProvider(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadProviderData()
	if err != nil {
		return err
	}
	filtered := data.DNSProviders[:0]
	for _, cfg := range data.DNSProviders {
		if cfg.ID != id {
			filtered = append(filtered, cfg)
		}
	}
	data.DNSProviders = filtered
	return s.saveProviderData(data)
}

// ListCertificates 返回托管证书列表，并隐藏私钥材料。
//
// 返回：
//   - 脱敏后的托管证书列表
//   - 读取或解析失败时返回错误
func (s *FileStore) ListCertificates() ([]ManagedCertificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadCertData()
	if err != nil {
		return nil, err
	}
	out := append([]ManagedCertificate(nil), data.Certificates...)
	for i := range out {
		redactCertificateMaterial(&out[i])
	}
	return out, nil
}

// GetCertificate 按 ID 读取包含完整 PEM 的托管证书。
//
// 参数：
//   - id: 托管证书 ID
//
// 返回：
//   - 命中的托管证书
//   - 是否存在
//   - 读取或解析失败时返回错误
func (s *FileStore) GetCertificate(id string) (ManagedCertificate, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadCertData()
	if err != nil {
		return ManagedCertificate{}, false, err
	}
	for _, cert := range data.Certificates {
		if cert.ID == id {
			return cert, true, nil
		}
	}
	return ManagedCertificate{}, false, nil
}

// UpsertCertificate 新增或覆盖托管证书。
//
// 参数：
//   - cert: 待保存的托管证书，ID 为空时会自动分配
//
// 返回：
//   - 保存后的托管证书，包含稳定 ID 和更新时间
//   - 读写失败时返回错误
func (s *FileStore) UpsertCertificate(cert ManagedCertificate) (ManagedCertificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadCertData()
	if err != nil {
		return ManagedCertificate{}, err
	}
	now := time.Now().UTC()
	if cert.ID == "" {
		cert.ID = uuid.NewString()
		cert.CreatedAt = now
	}
	cert.UpdatedAt = now
	for i, existing := range data.Certificates {
		if existing.ID == cert.ID {
			if cert.CreatedAt.IsZero() {
				cert.CreatedAt = existing.CreatedAt
			}
			data.Certificates[i] = cert
			return cert, s.saveCertData(data)
		}
	}
	if cert.CreatedAt.IsZero() {
		cert.CreatedAt = now
	}
	data.Certificates = append(data.Certificates, cert)
	return cert, s.saveCertData(data)
}

// DeleteCertificate 删除托管证书。
//
// 参数：
//   - id: 托管证书 ID
//
// 返回：
//   - 写入失败时返回错误
func (s *FileStore) DeleteCertificate(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadCertData()
	if err != nil {
		return err
	}
	filtered := data.Certificates[:0]
	for _, cert := range data.Certificates {
		if cert.ID != id {
			filtered = append(filtered, cert)
		}
	}
	data.Certificates = filtered
	return s.saveCertData(data)
}

// GetACMEAccount 读取全局 ACME 账号配置。
//
// 返回：
//   - ACME 账号配置，文件不存在时返回零值
//   - 读取或解析失败时返回错误
func (s *FileStore) GetACMEAccount() (ACMEAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadCertData()
	if err != nil {
		return ACMEAccount{}, err
	}
	return data.Account, nil
}

// SaveACMEAccount 保存全局 ACME 账号配置。
//
// 参数：
//   - acc: 待保存的账号配置
//
// 返回：
//   - 写入失败时返回错误
func (s *FileStore) SaveACMEAccount(acc ACMEAccount) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadCertData()
	if err != nil {
		return err
	}
	acc.UpdatedAt = time.Now().UTC()
	data.Account = acc
	return s.saveCertData(data)
}

func (s *FileStore) loadData() (fileStoreData, error) {
	data, err := os.ReadFile(s.ingressPath)
	if os.IsNotExist(err) {
		return fileStoreData{Ingresses: []Ingress{}, States: []AppliedState{}}, nil
	}
	if err != nil {
		return fileStoreData{}, err
	}
	var out fileStoreData
	if err := json.Unmarshal(data, &out); err != nil {
		return fileStoreData{}, err
	}
	return out, nil
}

func (s *FileStore) loadCertData() (certStoreData, error) {
	data, err := os.ReadFile(s.certsPath)
	if os.IsNotExist(err) {
		return certStoreData{Certificates: []ManagedCertificate{}}, nil
	}
	if err != nil {
		return certStoreData{}, err
	}
	var out certStoreData
	if err := json.Unmarshal(data, &out); err != nil {
		return certStoreData{}, err
	}
	return out, nil
}

func (s *FileStore) saveCertData(data certStoreData) error {
	if err := os.MkdirAll(filepath.Dir(s.certsPath), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(s.certsPath, raw, 0o600); err != nil {
		return err
	}
	return os.Chmod(s.certsPath, 0o600)
}

func (s *FileStore) saveData(data fileStoreData) error {
	if err := os.MkdirAll(filepath.Dir(s.ingressPath), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(s.ingressPath, raw, 0o644); err != nil {
		return err
	}
	return os.Chmod(s.ingressPath, 0o644)
}

func (s *FileStore) loadProviderData() (providerStoreData, error) {
	data, err := os.ReadFile(s.providersPath)
	if os.IsNotExist(err) {
		return providerStoreData{DNSProviders: []DNSProviderConfig{}}, nil
	}
	if err != nil {
		return providerStoreData{}, err
	}
	var out providerStoreData
	if err := json.Unmarshal(data, &out); err != nil {
		return providerStoreData{}, err
	}
	return out, nil
}

func (s *FileStore) saveProviderData(data providerStoreData) error {
	if err := os.MkdirAll(filepath.Dir(s.providersPath), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(s.providersPath, raw, 0o600); err != nil {
		return err
	}
	return os.Chmod(s.providersPath, 0o600)
}

func redactCertificateMaterial(cert *ManagedCertificate) {
	if cert.Material == nil {
		return
	}
	copied := *cert.Material
	copied.KeyPEM = ""
	cert.Material = &copied
}
