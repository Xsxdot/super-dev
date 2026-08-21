// Package buildinfo 提供发布构建元信息。
//
// 职责：
//   - 统一暴露 agent 与 MCP sidecar 的发布版本
//   - 让运行时 health、安装升级接口和协议握手返回同一个版本
//
// 边界：
//   - 不读取运行时配置
//   - 不负责版本号递增，版本源仍以仓库根目录 VERSION 文件为准
package buildinfo

// Version 是当前发布包内置的 agent/MCP 运行时版本。
//
// 注意：
//   - 发布前必须与仓库根目录 VERSION 保持一致
//   - scripts/check-version.mjs 会校验该值，防止打包元数据与 sidecar 版本不一致
const Version = "0.2.6"
