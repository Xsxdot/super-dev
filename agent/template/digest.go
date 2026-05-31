// Package template 中的 digest.go 负责生成模板内容摘要。
//
// 职责：
//   - 将模板序列化为稳定 YAML 字节
//   - 基于模板内容计算 sha256 digest
//
// 边界：
//   - 不读写模板文件
//   - 不判断版本发布策略
package template

import (
	"crypto/sha256"
	"encoding/hex"

	"gopkg.in/yaml.v3"
)

// Digest 返回模板内容的 sha256 摘要。
//
// 参数：
//   - t: 待计算摘要的模板
//
// 返回：
//   - `sha256:<hex>` 格式的摘要
//   - 模板无法序列化时返回错误
//
// 注意：
//   - digest 只表达当前模板内容，不负责判断版本是否可覆盖
func Digest(t Template) (string, error) {
	data, err := yaml.Marshal(t)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
