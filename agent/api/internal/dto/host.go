// Package dto 定义 Agent HTTP API 内部使用的请求与响应契约。
//
// 职责：
//   - 区分 Host 可写请求与不含秘密的安全读视图
//   - 为 API handler 与 Assembler 提供唯一字段合同
//
// 边界：
//   - 不依赖领域模型或持久化实现
//   - 不执行字段校验、默认值填充或秘密导入
package dto

// HostWrite 是 Host 创建与更新接口接受的可写字段。
//
// 注意：
//   - 空秘密在更新时表示保留原值，显式 clear 字段表示删除原值
//   - SSHKeyPath 只用于导入本机私钥文件，不会进入 Host 持久化模型
type HostWrite struct {
	ID                         string   `json:"id,omitempty"`
	Name                       string   `json:"name"`
	PublicIP                   string   `json:"public_ip,omitempty"`
	PrivateIP                  string   `json:"private_ip,omitempty"`
	Tags                       []string `json:"tags"`
	SSHHost                    string   `json:"ssh_host,omitempty"`
	SSHPort                    int      `json:"ssh_port,omitempty"`
	SSHUser                    string   `json:"ssh_user,omitempty"`
	SSHPassword                string   `json:"ssh_password,omitempty"`
	SSHPrivateKey              string   `json:"ssh_private_key,omitempty"`
	SSHKeyPath                 string   `json:"ssh_key_path,omitempty"`
	SSHHostKeyFingerprint      string   `json:"ssh_host_key_fingerprint,omitempty"`
	ClearSSHPassword           bool     `json:"clear_ssh_password,omitempty"`
	ClearSSHPrivateKey         bool     `json:"clear_ssh_private_key,omitempty"`
	ClearSSHHostKeyFingerprint bool     `json:"clear_ssh_host_key_fingerprint,omitempty"`
}

// HostView 是 Host HTTP API 返回的不含秘密的安全读视图。
//
// 注意：
//   - 只返回凭据与 fingerprint 是否已配置，永不返回秘密或原始 fingerprint
type HostView struct {
	ID                              string   `json:"id"`
	Name                            string   `json:"name"`
	PublicIP                        string   `json:"public_ip,omitempty"`
	PrivateIP                       string   `json:"private_ip,omitempty"`
	Tags                            []string `json:"tags"`
	SSHHost                         string   `json:"ssh_host,omitempty"`
	SSHPort                         int      `json:"ssh_port,omitempty"`
	SSHUser                         string   `json:"ssh_user,omitempty"`
	SSHCredentialConfigured         bool     `json:"ssh_credential_configured"`
	SSHPasswordConfigured           bool     `json:"ssh_password_configured"`
	SSHPrivateKeyConfigured         bool     `json:"ssh_private_key_configured"`
	SSHHostKeyFingerprintConfigured bool     `json:"ssh_host_key_fingerprint_configured"`
	IsSelf                          bool     `json:"is_self"`
	NodeID                          string   `json:"node_id,omitempty"`
}
