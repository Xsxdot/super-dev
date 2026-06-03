// Package manual 提供不会调用外部 API 的 DNS provider。
//
// 职责：
//   - 把 EnsureRecord 转成可展示给用户的人工操作指令
//
// 边界：
//   - 不真正创建、查询或删除 DNS 记录
//   - 不支持 ACME DNS-01 自动写入临时 TXT
package manual

import (
	"context"
	"fmt"

	"github.com/superdev/agent/ingress"
)

// Provider 返回人工 DNS 操作指令，不修改任何外部资源。
type Provider struct{}

// New 创建 manual DNS provider。
//
// 返回：
//   - manual Provider 实例
func New() *Provider {
	return &Provider{}
}

// Name 返回 provider 注册名。
//
// 返回：
//   - 固定值 manual
func (p *Provider) Name() string {
	return ingress.ProviderManual
}

// EnsureRecord 返回创建或更新 DNS 记录所需的人工操作指令。
//
// 参数：
//   - ctx: 上下文，保留给接口一致性使用
//   - record: 需要用户手动创建或更新的 DNS 记录
//
// 返回：
//   - 标记为 Manual 的 RecordResult
//   - 当前实现不会返回错误
func (p *Provider) EnsureRecord(ctx context.Context, record ingress.Record) (ingress.RecordResult, error) {
	return ingress.RecordResult{
		Record:  record,
		Manual:  true,
		Changed: false,
		Instructions: []string{
			fmt.Sprintf("Create DNS %s record %s -> %s with TTL %d", record.Type, record.Name, record.Value, record.TTL),
		},
	}, nil
}

// ListRecords 返回空列表。
//
// 参数：
//   - ctx: 上下文，保留给接口一致性使用
//   - domain: 待查询域名
//
// 返回：
//   - 空 DNS 记录列表
//   - 当前实现不会返回错误
//
// 注意：
//   - manual provider 不读取真实 DNS，所以无法参与 DNS 孤儿资源检测
func (p *Provider) ListRecords(ctx context.Context, domain string) ([]ingress.Record, error) {
	return []ingress.Record{}, nil
}

// RemoveRecord 拒绝自动删除 DNS 记录。
//
// 参数：
//   - ctx: 上下文，保留给接口一致性使用
//   - record: 待删除 DNS 记录
//
// 返回：
//   - 始终返回错误，要求用户自行处理
func (p *Provider) RemoveRecord(ctx context.Context, record ingress.Record) error {
	return fmt.Errorf("manual DNS provider cannot remove records automatically: %s", record.Name)
}
