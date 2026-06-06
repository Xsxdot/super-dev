// Package store 中的 runs.go 提供 pipeline run 历史和日志持久化。
//
// 职责：
//   - 保存 Run 终态和中间态快照
//   - 按 project/pipeline 查询部署历史
//   - 按 run/step/host 保存与读取日志行
//
// 边界：
//   - 不执行 pipeline，不计算 DAG
//   - 不做日志读取过滤之外的业务判断
package store

import (
	"database/sql"
	"encoding/json"

	"github.com/xsxdot/super-dev/agent/model"
)

// RunLogQuery 定义 pipeline run 日志读取过滤条件。
type RunLogQuery struct {
	RunID     string
	StepName  string
	HostID    string
	Limit     int
	BeforeID  int64
	AfterID   int64
	Ascending bool
}

// SaveRun 插入或更新一次 pipeline run 快照。
func (s *Store) SaveRun(run model.Run) error {
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT OR REPLACE INTO pipeline_runs
			(id, project_id, pipeline_id, env_name, deployment_id, artifact_version, status, started_at, finished_at, run_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, run.ID, run.ProjectID, run.PipelineID, run.EnvName, run.DeploymentID, run.ArtifactVersion,
		string(run.Status), run.StartedAt, run.FinishedAt, string(data))
	return err
}

// GetRun 按 run ID 返回 Run 快照。
func (s *Store) GetRun(id string) (model.Run, bool, error) {
	var data string
	err := s.db.QueryRow(`SELECT run_json FROM pipeline_runs WHERE id = ?`, id).Scan(&data)
	if err == sql.ErrNoRows {
		return model.Run{}, false, nil
	}
	if err != nil {
		return model.Run{}, false, err
	}
	run, err := decodeRun(data)
	if err != nil {
		return model.Run{}, false, err
	}
	return run, true, nil
}

// ListRuns 按 project/pipeline 返回最近的 Run 历史。
func (s *Store) ListRuns(projectID, pipelineID string) ([]model.Run, error) {
	rows, err := s.db.Query(`
		SELECT run_json
		FROM pipeline_runs
		WHERE project_id = ? AND pipeline_id = ?
		ORDER BY started_at DESC, id DESC
	`, projectID, pipelineID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []model.Run
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		run, err := decodeRun(data)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return runs, nil
}

// AppendRunLogLine 追加一条 step/host 维度的日志，并返回带数据库自增 ID 的日志行。
func (s *Store) AppendRunLogLine(runID, stepName, hostID, stream, line string, at int64) (model.RunLogLine, error) {
	entry := model.RunLogLine{
		RunID:    runID,
		StepName: stepName,
		HostID:   hostID,
		Stream:   stream,
		Line:     line,
		At:       at,
	}
	result, err := s.db.Exec(`
		INSERT INTO pipeline_run_logs (run_id, step_name, host_id, stream, line, at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, runID, stepName, hostID, stream, line, at)
	if err != nil {
		return model.RunLogLine{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return model.RunLogLine{}, err
	}
	entry.ID = id
	return entry, nil
}

// AppendRunLog 追加一条 step/host 维度的日志。
func (s *Store) AppendRunLog(runID, stepName, hostID, stream, line string, at int64) error {
	_, err := s.AppendRunLogLine(runID, stepName, hostID, stream, line, at)
	return err
}

// ReadRunLogs 按 run/step/host 读取日志。
func (s *Store) ReadRunLogs(q RunLogQuery) ([]model.RunLogLine, error) {
	if q.Limit <= 0 {
		q.Limit = 1000
	}
	query := `
		SELECT id, run_id, step_name, host_id, stream, line, at
		FROM pipeline_run_logs
		WHERE run_id = ?
	`
	args := []any{q.RunID}
	if q.StepName != "" {
		query += " AND step_name = ?"
		args = append(args, q.StepName)
	}
	if q.HostID != "" {
		query += " AND host_id = ?"
		args = append(args, q.HostID)
	}
	if q.BeforeID > 0 {
		query += " AND id < ?"
		args = append(args, q.BeforeID)
	}
	if q.AfterID > 0 {
		query += " AND id > ?"
		args = append(args, q.AfterID)
	}
	order := "DESC"
	if q.Ascending {
		order = "ASC"
	}
	query += " ORDER BY id " + order + " LIMIT ?"
	args = append(args, q.Limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var lines []model.RunLogLine
	for rows.Next() {
		var line model.RunLogLine
		if err := rows.Scan(&line.ID, &line.RunID, &line.StepName, &line.HostID, &line.Stream, &line.Line, &line.At); err != nil {
			return nil, err
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !q.Ascending {
		for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
			lines[i], lines[j] = lines[j], lines[i]
		}
	}
	return lines, nil
}

func decodeRun(data string) (model.Run, error) {
	var run model.Run
	if err := json.Unmarshal([]byte(data), &run); err != nil {
		return model.Run{}, err
	}
	return run, nil
}
