// Package model 中的 artifact.go 定义部署制品的可追溯数据模型。
//
// 职责：
//   - 声明制品类型、制品引用和持久化日志行模型
//   - 为 ArtifactStore、RunStore、API 和 MCP 提供统一 DTO
//
// 边界：
//   - 不复制文件、不访问 SQLite、不执行部署
//   - 不按语言或框架区分制品，部署阶段只认 file 与 image
package model

// ArtifactKind 表示部署阶段可追溯的制品类型。
type ArtifactKind string

const (
	// ArtifactKindFile 表示 tar.gz、zip、binary 等本地文件制品。
	ArtifactKindFile ArtifactKind = "file"
	// ArtifactKindImage 表示 Docker registry 中的 image:tag。
	ArtifactKindImage ArtifactKind = "image"
)

// ArtifactRef 指向一次可重新取回的构建产物。
type ArtifactRef struct {
	Version   string            `json:"version"`
	Kind      ArtifactKind      `json:"kind"`
	Location  string            `json:"location"`
	Meta      map[string]string `json:"meta,omitempty"`
	CreatedAt int64             `json:"created_at"`
}

// RunLogLine 是 pipeline run 日志的持久化视图。
type RunLogLine struct {
	ID       int64  `json:"id"`
	RunID    string `json:"run_id"`
	StepName string `json:"step_name"`
	HostID   string `json:"host_id,omitempty"`
	Stream   string `json:"stream"`
	Line     string `json:"line"`
	At       int64  `json:"at"`
}
