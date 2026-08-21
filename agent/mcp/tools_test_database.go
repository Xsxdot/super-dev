// tools_test_database.go 实现临时测试数据库 MCP 四原语。
//
// 职责：
//   - 把 AI 的真实数据库申请、续租、回收与列表请求转发给本机 agent
//   - 在 acquire 失败时复用统一审批轮询流程
//
// 边界：
//   - 本文件是明文 DSN 的唯一出口；任何日志与审计都不得复制 DSN 或密码
//   - 不直接访问 PG、Redis、SQLite 或项目配置
package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/dbprovision"
)

const acquireTestDatabaseDescription = "Acquire an isolated real test environment (a PostgreSQL database cloned from the project's dev database, plus a free Redis db) and return plaintext connection strings. **Use this whenever tests need a real database — never fall back to sqlite or an in-memory substitute.** The PG database is a clone of the project's dev database, so it already has schema and seed data. Auto-reclaimed at expiry; call release_test_database when done. Requires project and purpose."

func acquireTestDatabaseInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"project_id":            map[string]any{"type": "string"},
			"purpose":               map[string]any{"type": "string"},
			"ttl_minutes":           map[string]any{"type": "integer", "minimum": 0},
			"kinds":                 map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"approval_token":        map[string]any{"type": "string"},
			"approval_wait_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 300},
		},
		"required": []string{"project_id", "purpose"},
	}
}

func testDatabaseLeaseInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{"lease_id": map[string]any{"type": "string"}},
		"required":             []string{"lease_id"},
	}
}

func renewTestDatabaseInputSchema() map[string]any {
	schema := testDatabaseLeaseInputSchema()
	properties := schema["properties"].(map[string]any)
	properties["ttl_minutes"] = map[string]any{"type": "integer", "minimum": 0}
	return schema
}

func (s *Server) acquireTestDatabaseTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		ProjectID           string   `json:"project_id"`
		Purpose             string   `json:"purpose"`
		TTLMinutes          int      `json:"ttl_minutes"`
		Kinds               []string `json:"kinds"`
		ApprovalToken       string   `json:"approval_token"`
		ApprovalWaitSeconds *int     `json:"approval_wait_seconds"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.Purpose = strings.TrimSpace(req.Purpose)
	req.ApprovalToken = strings.TrimSpace(req.ApprovalToken)
	if req.ProjectID == "" {
		return toolError("invalid_arguments", "project_id is required", nil), nil
	}
	if req.Purpose == "" {
		return toolError("invalid_arguments", "purpose is required", nil), nil
	}
	if req.TTLMinutes < 0 {
		return toolError("invalid_arguments", "ttl_minutes must not be negative", nil), nil
	}
	log := logger.GetLogger().WithEntryName("MCPTestDatabase").WithFields(map[string]any{
		"tool": "acquire_test_database", "project": req.ProjectID, "purpose": req.Purpose,
	})
	log.Info("临时测试资源工具进入")

	request := TestDatabaseAcquireRequest{Purpose: req.Purpose, TTLMinutes: req.TTLMinutes, Kinds: req.Kinds}
	var lease dbprovision.Lease
	acquire := func(ctx context.Context, token string) error {
		got, err := s.client.AcquireTestDatabase(ctx, req.ProjectID, request, token)
		if err != nil {
			if approval, ok := approvalRequiredAgentError(err); ok {
				logger.GetLogger().WithEntryName("MCPTestDatabase").WithFields(map[string]any{
					"approval_id": approval.Approval.ID, "wait_seconds": boundedApprovalWait(req.ApprovalWaitSeconds).Seconds(),
				}).Info("临时测试资源等待审批")
			}
			return err
		}
		lease = got
		return nil
	}
	var err error
	if req.ApprovalToken != "" {
		err = acquire(ctx, req.ApprovalToken)
	} else {
		err = s.callWithApproval(ctx, boundedApprovalWait(req.ApprovalWaitSeconds), acquire)
	}
	if err != nil {
		if approval, ok := approvalRequiredAgentError(err); ok {
			logger.GetLogger().WithEntryName("MCPTestDatabase").WithField("approval_id", approval.Approval.ID).Warn("临时测试资源审批未完成")
		}
		log.WithErr(err).WithField("status", "error").Error("临时测试资源申请失败")
		return clientToolError(err), nil
	}
	resourceNames := make([]string, 0, len(lease.Resources))
	for _, resource := range lease.Resources {
		resourceNames = append(resourceNames, resource.Name)
	}
	logger.GetLogger().WithEntryName("MCPTestDatabase").WithFields(map[string]any{
		"lease_id": lease.ID, "resource_names": resourceNames, "expires_at": lease.ExpiresAt,
	}).Info("临时测试资源申请成功")
	return toolSuccess("test database environment acquired", map[string]any{"lease": lease}, nil, nil), nil
}

func (s *Server) releaseTestDatabaseTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		LeaseID string `json:"lease_id"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.LeaseID = strings.TrimSpace(req.LeaseID)
	if req.LeaseID == "" {
		return toolError("invalid_arguments", "lease_id is required", nil), nil
	}
	logger.GetLogger().WithEntryName("MCPTestDatabase").WithFields(map[string]any{
		"tool": "release_test_database", "project": "", "purpose": "",
	}).Info("临时测试资源回收工具进入")
	if err := s.client.ReleaseTestDatabase(ctx, req.LeaseID); err != nil {
		logger.GetLogger().WithEntryName("MCPTestDatabase").WithErr(err).WithFields(map[string]any{
			"tool": "release_test_database", "status": "error",
		}).Error("临时测试资源回收失败")
		return clientToolError(err), nil
	}
	return toolSuccess("test database environment released", map[string]any{"lease_id": req.LeaseID}, nil, nil), nil
}

func (s *Server) renewTestDatabaseTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		LeaseID    string `json:"lease_id"`
		TTLMinutes int    `json:"ttl_minutes"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.LeaseID = strings.TrimSpace(req.LeaseID)
	if req.LeaseID == "" {
		return toolError("invalid_arguments", "lease_id is required", nil), nil
	}
	if req.TTLMinutes < 0 {
		return toolError("invalid_arguments", "ttl_minutes must not be negative", nil), nil
	}
	lease, err := s.client.RenewTestDatabase(ctx, req.LeaseID, TestDatabaseRenewRequest{TTLMinutes: req.TTLMinutes})
	if err != nil {
		logger.GetLogger().WithEntryName("MCPTestDatabase").WithErr(err).WithFields(map[string]any{
			"tool": "renew_test_database", "status": "error",
		}).Error("临时测试资源续租失败")
		return clientToolError(err), nil
	}
	return toolSuccess("test database environment renewed", map[string]any{"lease": lease}, nil, nil), nil
}

func (s *Server) listTestDatabasesTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	if err := decodeToolArgs(args, &struct{}{}); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	leases, err := s.client.ListTestDatabases(ctx)
	if err != nil {
		logger.GetLogger().WithEntryName("MCPTestDatabase").WithErr(err).WithFields(map[string]any{
			"tool": "list_test_databases", "status": "error",
		}).Error("临时测试资源列表读取失败")
		return clientToolError(err), nil
	}
	return toolSuccess("test database environments loaded", map[string]any{"leases": leases, "count": len(leases)}, nil, nil), nil
}
