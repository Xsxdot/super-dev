// Package dbprovision 提供 AI 临时测试资源的供给层。
//
// 职责：
//   - 登记 PG / Redis 等实例的管理连接（Registry）
//   - 按资源类型插件化地开/收临时资源（Provisioner）
//   - 以 TTL 租约管理临时资源的生命周期与配额（LeaseManager）
//
// 边界：
//   - 不做数据库纳管、安装、监控——供给层与纳管彻底解耦
//   - 不持有任何 HTTP/MCP 概念，鉴权与审批由调用方（api / mcp 层）负责
//   - 明文凭据只在 Resource.DSN 中向调用方返回一次，本包不写入日志
package dbprovision

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// ResourcePrefix 是所有临时资源标识的强制前缀。
//
// 注意：它是登记表丢失时唯一的兜底识别依据（见 postgres.go 的 Reconcile），
// 因此不可配置、不可随版本变更——改它等于让历史遗留资源永远无法被对账回收。
const ResourcePrefix = "sdev_eph_"

// maxProjectSlugLen 限制名字中项目片段的长度。
// 前缀 9 + slug 20 + 下划线 1 + 随机 12 = 42，安全落在 PG 标识符 63 字节上限内。
const maxProjectSlugLen = 20

// NewResourceName 生成一个临时资源标识。
//
// 参数：
//   - projectName: 项目展示名，仅用于让名字可读，会被规范化并截断
//
// 返回：
//   - 形如 sdev_eph_<slug>_<12位十六进制> 的标识；随机源不可用时返回错误
//
// 注意：
//   - 结果只含 [a-z0-9_]，可直接作 PG 库名与角色名，无需再加引号
func NewResourceName(projectName string) (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成临时资源名的随机后缀失败: %w", err)
	}
	return ResourcePrefix + projectSlug(projectName) + "_" + hex.EncodeToString(buf), nil
}

// projectSlug 把项目名规范化为 [a-z0-9_] 片段并截断。
// 空结果回退为 "proj"：名字里没有项目痕迹也比生成非法标识符强。
func projectSlug(projectName string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(projectName) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= maxProjectSlugLen {
			break
		}
	}
	slug := strings.Trim(b.String(), "_")
	if slug == "" {
		return "proj"
	}
	return slug
}
