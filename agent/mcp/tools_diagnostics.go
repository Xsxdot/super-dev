// tools_diagnostics.go 实现服务诊断聚合工具。
//
// 职责：
//   - 聚合 deployment 状态、runtime 摘要和最近日志证据
//   - 返回结构化 evidence 与轻量 hints
//
// 边界：
//   - 不断言根因
//   - 不执行启停或配置修改
package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/superdev/agent/model"
)

func (s *Server) diagnoseServiceTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req targetArgs
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	projects, err := s.client.ListProjects(ctx)
	if err != nil {
		return clientToolError(err), nil
	}
	target, errResp := resolveDeploymentTarget(projects, req)
	if errResp != nil {
		return toolError(errResp.Code, errResp.Message, errResp), nil
	}
	services, err := s.client.ListServices(ctx)
	if err != nil {
		return clientToolError(err), nil
	}
	status := serviceStatusKey(target.Deployment.Status)
	if runtimeSvc, runtimeDep, ok := findRuntimeDeployment(services, target.Deployment.ID); ok {
		status = serviceStatusKey(runtimeDep.Status)
		if runtimeDep.Status == "" && runtimeSvc.Status != "" {
			status = serviceStatusKey(runtimeSvc.Status)
		}
		target.Deployment.Status = runtimeDep.Status
		target.Deployment.PID = runtimeDep.PID
	}

	query := url.Values{}
	query.Set("limit", strconv.Itoa(defaultLogToolLimit))
	logs, err := s.client.FetchDeploymentLogs(ctx, target.Deployment.ID, query)
	if err != nil {
		return clientToolError(err), nil
	}
	evidence := diagnosisEvidence(status, logs.Items)
	hints := diagnosisHints(status, target.Deployment, logs.Items)
	data := map[string]any{
		"target":          sanitizeTarget(target),
		"status":          status,
		"runtime_summary": runtimeSummary(target.Deployment),
		"evidence":        evidence,
		"hints":           hints,
	}
	return toolSuccess("diagnosis evidence collected", data, nil, nil), nil
}

func findRuntimeDeployment(services []model.Service, deploymentID string) (model.Service, model.Deployment, bool) {
	for _, service := range services {
		for _, deployment := range service.Deployments {
			if deployment.ID == deploymentID {
				return service, deployment, true
			}
		}
	}
	return model.Service{}, model.Deployment{}, false
}

func diagnosisEvidence(status string, entries []model.LogEntry) []map[string]any {
	evidence := []map[string]any{}
	if status == string(model.StatusFailed) {
		evidence = append(evidence, map[string]any{"type": "status", "message": "deployment status is failed"})
	}
	for _, entry := range entries {
		if !isErrorEvidence(entry) {
			continue
		}
		evidence = append(evidence, map[string]any{
			"type":       "log",
			"id":         entry.ID,
			"level":      entry.Level,
			"message":    entry.Message,
			"timestamp":  entry.Timestamp,
			"stream":     entry.Stream,
			"deployment": entry.DeploymentID,
		})
		if len(evidence) >= 20 {
			break
		}
	}
	return evidence
}

func isErrorEvidence(entry model.LogEntry) bool {
	level := strings.ToUpper(entry.Level)
	if level == "ERROR" || level == "FATAL" || level == "PANIC" {
		return true
	}
	message := strings.ToLower(entry.Message)
	return strings.Contains(message, "panic") || strings.Contains(message, "fatal")
}

func diagnosisHints(status string, deployment model.Deployment, entries []model.LogEntry) []string {
	hints := []string{}
	if status == string(model.StatusFailed) {
		hints = append(hints, "检查启动命令、工作目录、端口占用和最近 ERROR 日志")
	}
	if len(entries) == 0 {
		hints = append(hints, "该 deployment 没有可用日志，先确认日志来源配置或尝试重启后再 tail_logs")
	}
	if deployment.IsReadOnly() {
		hints = append(hints, "该 deployment 来自远程或外部日志源，只能观察，不能由 MCP 启停")
	}
	if len(hints) == 0 {
		hints = append(hints, "未发现明确失败信号，可继续使用 tail_logs 或 search_logs 收集更多证据")
	}
	return hints
}

func runtimeSummary(deployment model.Deployment) map[string]any {
	sanitized := sanitizeDeployment(deployment)
	return map[string]any{
		"location":     sanitized.Location,
		"control_mode": sanitized.EffectiveControlMode(),
		"runtime":      sanitized.Runtime,
		"command":      sanitized.Command,
		"work_dir":     sanitized.WorkDir,
		"env":          sanitized.Env,
		"pid":          sanitized.PID,
	}
}
