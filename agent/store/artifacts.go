// Package store 中的 artifacts.go 提供本地制品仓库能力。
//
// 职责：
//   - 将 file 制品复制到 agent data dir 下的 artifacts 目录
//   - 将 image 制品登记为 registry tag，不复制 body
//   - 保存、查询、清理制品元数据
//
// 边界：
//   - 不执行构建命令，不解析 pipeline 模板
//   - 不决定部署或回滚策略，只保存可追溯引用
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

// ErrArtifactNotFound 表示指定版本制品不存在。
//
// 用独立 error 值而非 sql.ErrNoRows 别名，避免把底层存储实现泄露给调用方，
// 为后续 S3/OSS 后端保留替换空间。GetArtifact 内部把 sql.ErrNoRows 转译为此值。
var ErrArtifactNotFound = errors.New("artifact not found")

// PutArtifact 保存或登记一个制品版本。
func (s *Store) PutArtifact(ctx context.Context, projectID, pipelineID string, ref model.ArtifactRef, body io.Reader) (model.ArtifactRef, error) {
	if projectID == "" || pipelineID == "" || ref.Version == "" {
		return model.ArtifactRef{}, errors.New("project_id, pipeline_id and version are required")
	}
	if ref.Kind == "" {
		ref.Kind = model.ArtifactKindFile
	}
	if ref.CreatedAt == 0 {
		ref.CreatedAt = time.Now().UnixMilli()
	}
	if ref.Kind == model.ArtifactKindFile {
		if body == nil {
			return model.ArtifactRef{}, errors.New("file artifact body is required")
		}
		dir := filepath.Join(s.artifactRoot, projectID, pipelineID, ref.Version)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return model.ArtifactRef{}, err
		}
		dst := filepath.Join(dir, "artifact")
		out, err := os.Create(dst)
		if err != nil {
			return model.ArtifactRef{}, err
		}
		if _, err := io.Copy(out, body); err != nil {
			_ = out.Close()
			return model.ArtifactRef{}, err
		}
		if err := out.Close(); err != nil {
			return model.ArtifactRef{}, err
		}
		ref.Location = dst
	}
	if ref.Kind == model.ArtifactKindImage && ref.Location == "" {
		return model.ArtifactRef{}, errors.New("image artifact location is required")
	}
	meta, err := json.Marshal(ref.Meta)
	if err != nil {
		return model.ArtifactRef{}, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO pipeline_artifacts
			(project_id, pipeline_id, version, kind, location, meta_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, projectID, pipelineID, ref.Version, string(ref.Kind), ref.Location, string(meta), ref.CreatedAt)
	if err != nil {
		return model.ArtifactRef{}, fmt.Errorf("save artifact metadata: %w", err)
	}
	return ref, nil
}

// GetArtifact 返回指定版本制品。
func (s *Store) GetArtifact(ctx context.Context, projectID, pipelineID, version string) (model.ArtifactRef, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT version, kind, location, meta_json, created_at
		FROM pipeline_artifacts
		WHERE project_id = ? AND pipeline_id = ? AND version = ?
	`, projectID, pipelineID, version)
	ref, err := scanArtifact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ArtifactRef{}, ErrArtifactNotFound
	}
	if err != nil {
		return model.ArtifactRef{}, err
	}
	return ref, nil
}

// ListArtifacts 按 created_at DESC 返回某条项目流水线的制品历史。
func (s *Store) ListArtifacts(ctx context.Context, projectID, pipelineID string) ([]model.ArtifactRef, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT version, kind, location, meta_json, created_at
		FROM pipeline_artifacts
		WHERE project_id = ? AND pipeline_id = ?
		ORDER BY created_at DESC, version DESC
	`, projectID, pipelineID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var refs []model.ArtifactRef
	for rows.Next() {
		ref, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return refs, nil
}

// PruneArtifacts 只保留最近 keep 个制品，keep <= 0 时清理全部。
func (s *Store) PruneArtifacts(ctx context.Context, projectID, pipelineID string, keep int) error {
	query := `
		SELECT version, kind, location, meta_json, created_at
		FROM pipeline_artifacts
		WHERE project_id = ? AND pipeline_id = ?
		ORDER BY created_at DESC, version DESC
	`
	args := []any{projectID, pipelineID}
	if keep > 0 {
		query += " LIMIT -1 OFFSET ?"
		args = append(args, keep)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	var refs []model.ArtifactRef
	for rows.Next() {
		ref, err := scanArtifact(rows)
		if err != nil {
			_ = rows.Close()
			return err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(refs) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM pipeline_artifacts
			WHERE project_id = ? AND pipeline_id = ? AND version = ?
		`, projectID, pipelineID, ref.Version); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	for _, ref := range refs {
		if ref.Kind == model.ArtifactKindFile && isStoreArtifactPath(ref.Location, s.artifactRoot) {
			_ = os.Remove(ref.Location)
		}
	}
	return nil
}

type artifactScanner interface {
	Scan(dest ...any) error
}

func scanArtifact(scanner artifactScanner) (model.ArtifactRef, error) {
	var ref model.ArtifactRef
	var kind string
	var metaJSON string
	if err := scanner.Scan(&ref.Version, &kind, &ref.Location, &metaJSON, &ref.CreatedAt); err != nil {
		return model.ArtifactRef{}, err
	}
	ref.Kind = model.ArtifactKind(kind)
	if metaJSON != "" && metaJSON != "null" {
		if err := json.Unmarshal([]byte(metaJSON), &ref.Meta); err != nil {
			return model.ArtifactRef{}, err
		}
	}
	return ref, nil
}

func isStoreArtifactPath(location, artifactRoot string) bool {
	if location == "" || artifactRoot == "" {
		return false
	}
	cleanLocation := filepath.Clean(location)
	cleanRoot := filepath.Clean(artifactRoot)
	return strings.HasPrefix(cleanLocation, cleanRoot+string(os.PathSeparator))
}
