// postgres.go —— PostgreSQL 临时数据库供给器的管理连接探测实现。
//
// 职责：使用管理连接探测 PostgreSQL 版本、建库/建角色/断连能力，并为后续 Plan/Provision
// 提供安全的管理员 DSN 拼装能力。
// 边界：不管理租约、配额或项目绑定；资源生命周期动作将在后续方法中实现，明文凭据不写日志。
package dbprovision

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/xsxdot/gokit/logger"
)

// PostgresProvisioner 是 PostgreSQL 临时库供给器。
//
// 注意：该类型无状态，所有操作都现连现关，便于低频供给动作隔离连接生命周期。
type PostgresProvisioner struct{}

type postgresActiveConnection struct {
	pid         int32
	application string
	user        string
	state       string
}

// NewPostgresProvisioner 创建一个 PostgreSQL 供给器。
func NewPostgresProvisioner() *PostgresProvisioner {
	return &PostgresProvisioner{}
}

func init() {
	RegisterProvisioner(NewPostgresProvisioner())
}

// Kind 返回 PostgreSQL 资源类型标识。
func (p *PostgresProvisioner) Kind() string { return KindPostgres }

// Probe 探测 PostgreSQL 管理连接、版本和临时库供给所需权限。
//
// 返回值 error 只表示探测流程本身无法继续；探测结论（包括连接失败、版本过低或权限缺失）
// 写在 ProbeResult.OK、Error、Missing 与 FixHint 中，便于调用方把可修复信息展示给用户。
func (p *PostgresProvisioner) Probe(ctx context.Context, ds DataSource) (ProbeResult, error) {
	log := logger.GetLogger().WithEntryName("DBProvisionPG").WithFields(map[string]any{
		"host": ds.Host, "port": ds.Port, "user": ds.User,
	})
	log.Debug("开始探测 PostgreSQL 管理连接")
	result := ProbeResult{CheckedAt: time.Now(), Capabilities: map[string]bool{}, Facts: map[string]string{}}
	conn, err := pgx.Connect(ctx, adminDSN(ds, ""))
	if err != nil {
		result.Error = err.Error()
		log.WithErr(err).Error("连接 PostgreSQL 管理库失败")
		return result, nil
	}
	defer conn.Close(ctx)

	var version string
	if err := conn.QueryRow(ctx, `SELECT version()`).Scan(&version); err != nil {
		result.Error = err.Error()
		log.WithErr(err).Error("查询 PostgreSQL 版本失败")
		return result, nil
	}
	result.ServerVer = version
	major, err := postgresMajorVersion(version)
	if err != nil {
		result.Error = fmt.Sprintf("无法解析 PostgreSQL 版本: %v", err)
		log.WithErr(err).Error("解析 PostgreSQL 版本失败")
		return result, nil
	}
	if major < 13 {
		result.Error = "需要 PostgreSQL 13+（DROP DATABASE ... WITH (FORCE)）"
		log.WithField("server_version", version).Error("PostgreSQL 版本不满足临时库供给要求")
		return result, nil
	}

	var createdb, createrole bool
	if err := conn.QueryRow(ctx, `
		SELECT rolcreatedb, rolcreaterole
		FROM pg_roles WHERE rolname = current_user
	`).Scan(&createdb, &createrole); err != nil {
		result.Error = err.Error()
		log.WithErr(err).Error("查询 PostgreSQL 当前角色能力失败")
		return result, nil
	}
	var signalBackend bool
	if err := conn.QueryRow(ctx, `SELECT pg_has_role(current_user, 'pg_signal_backend', 'member')`).Scan(&signalBackend); err != nil {
		result.Error = err.Error()
		log.WithErr(err).Error("查询 PostgreSQL 断连能力失败")
		return result, nil
	}
	result.Capabilities["createdb"] = createdb
	result.Capabilities["createrole"] = createrole
	result.Capabilities["pg_signal_backend"] = signalBackend
	if !createdb {
		result.Missing = append(result.Missing, "createdb")
	}
	if !createrole {
		result.Missing = append(result.Missing, "createrole")
	}
	if !signalBackend {
		result.Missing = append(result.Missing, "pg_signal_backend")
	}
	var hints []string
	role := pgx.Identifier{ds.User}.Sanitize()
	if !createdb {
		hints = append(hints, "ALTER ROLE "+role+" CREATEDB;")
	}
	if !createrole {
		hints = append(hints, "ALTER ROLE "+role+" CREATEROLE;")
	}
	if !signalBackend {
		hints = append(hints, "GRANT pg_signal_backend TO "+role+";")
	}
	result.FixHint = strings.Join(hints, "\n")
	result.OK = len(result.Missing) == 0
	log.WithFields(map[string]any{
		"host": ds.Host, "port": ds.Port, "ok": result.OK, "missing": result.Missing,
	}).Info("PostgreSQL 管理连接探测完成")
	return result, nil
}

// Plan 计算 PostgreSQL 临时库克隆计划。
//
// 注意：本方法只读，不得产生副作用；断开连接只在 SideEffects 中声明，实际执行由 Provision
// 在审批门禁通过后完成。返回值 error 表示计划无法安全计算，例如模板不存在或模板繁忙且禁用断连。
func (p *PostgresProvisioner) Plan(ctx context.Context, ds DataSource, req PlanRequest) (Plan, error) {
	binding := req.Binding.Postgres
	if binding == nil || strings.TrimSpace(binding.DevDatabase) == "" {
		return Plan{}, ErrBindingMissing
	}
	log := logger.GetLogger().WithEntryName("DBProvisionPG").WithFields(map[string]any{
		"project_id": req.ProjectID, "template": binding.DevDatabase,
	})
	log.Debug("开始计算 PostgreSQL 临时库计划")
	conn, err := pgx.Connect(ctx, adminDSN(ds, ""))
	if err != nil {
		log.WithErr(err).Error("连接 PostgreSQL 维护库失败")
		return Plan{}, fmt.Errorf("连接 PostgreSQL 维护库失败: %w", err)
	}
	defer conn.Close(ctx)

	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, binding.DevDatabase).Scan(&exists); err != nil {
		log.WithErr(err).Error("校验 PostgreSQL 模板库失败")
		return Plan{}, fmt.Errorf("校验模板库失败: %w", err)
	}
	if !exists {
		err := fmt.Errorf("模板库不存在: %s", binding.DevDatabase)
		log.WithField("template", binding.DevDatabase).WithErr(err).Error("PostgreSQL 模板库不存在")
		return Plan{}, err
	}

	var templateSize string
	if err := conn.QueryRow(ctx, `SELECT pg_size_pretty(pg_database_size($1))`, binding.DevDatabase).Scan(&templateSize); err != nil {
		log.WithErr(err).Error("查询 PostgreSQL 模板库体积失败")
		return Plan{}, fmt.Errorf("查询模板库体积失败: %w", err)
	}
	// 必须排除当前用于探测的连接，否则这个 Plan 会永远把自己视为占用者并自我阻塞。
	rows, err := conn.Query(ctx, `
		SELECT pid, coalesce(application_name, ''), coalesce(usename, ''), coalesce(state, '')
		FROM pg_stat_activity
		WHERE datname = $1 AND pid <> pg_backend_pid()
	`, binding.DevDatabase)
	if err != nil {
		log.WithErr(err).Error("查询 PostgreSQL 模板库活跃连接失败")
		return Plan{}, fmt.Errorf("查询模板库活跃连接失败: %w", err)
	}
	var connections []postgresActiveConnection
	for rows.Next() {
		var item postgresActiveConnection
		if err := rows.Scan(&item.pid, &item.application, &item.user, &item.state); err != nil {
			rows.Close()
			log.WithErr(err).Error("读取 PostgreSQL 活跃连接失败")
			return Plan{}, fmt.Errorf("读取模板库活跃连接失败: %w", err)
		}
		connections = append(connections, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		log.WithErr(err).Error("遍历 PostgreSQL 活跃连接失败")
		return Plan{}, fmt.Errorf("遍历模板库活跃连接失败: %w", err)
	}
	rows.Close()

	plan := Plan{
		Kind:         KindPostgres,
		ResourceName: req.NameSeed,
		Detail: map[string]string{
			"template":      binding.DevDatabase,
			"template_size": templateSize,
		},
		Steps: []string{
			fmt.Sprintf("克隆 %s → %s", binding.DevDatabase, req.NameSeed),
			"创建临时角色并仅授本库权限",
		},
	}
	if len(connections) > 0 {
		log.WithFields(map[string]any{
			"template": binding.DevDatabase, "active_conns": len(connections),
			"terminate_enabled": binding.TerminateConnections,
		}).Info("探测到 PostgreSQL 模板库活跃连接")
		if !binding.TerminateConnections {
			occupants := connectionDetails(connections, 5)
			err := fmt.Errorf("%w: 模板库 %s 有 %d 个活跃连接（%s）", ErrTemplateBusy, binding.DevDatabase, len(connections), occupants)
			log.WithFields(map[string]any{"template": binding.DevDatabase, "active_conns": len(connections)}).WithErr(err).Warn("模板库繁忙且禁用断连，拒绝计划")
			return Plan{}, err
		}
		plan.SideEffects = []SideEffect{{
			Kind:   SideEffectTerminateConnections,
			Target: binding.DevDatabase,
			Detail: connectionDetails(connections, 5),
			Count:  len(connections),
		}}
		plan.Steps = append([]string{fmt.Sprintf("断开 %s 上 %d 个活跃连接", binding.DevDatabase, len(connections))}, plan.Steps...)
	}
	log.WithFields(map[string]any{
		"resource_name": plan.ResourceName, "side_effects": len(plan.SideEffects),
		"template_size": templateSize,
	}).Info("PostgreSQL 计划计算完成")
	return plan, nil
}

// Provision 由后续任务实现；当前骨架不创建任何真实资源。
func (p *PostgresProvisioner) Provision(context.Context, DataSource, Plan) (Resource, error) {
	return Resource{}, fmt.Errorf("PostgreSQL Provision 尚未实现")
}

// Reclaim 由后续任务实现；当前骨架不执行任何回收动作。
func (p *PostgresProvisioner) Reclaim(context.Context, DataSource, Resource) error {
	return fmt.Errorf("PostgreSQL Reclaim 尚未实现")
}

// Reconcile 由后续任务实现；当前骨架不扫描或回收任何数据库。
func (p *PostgresProvisioner) Reconcile(context.Context, DataSource, []Resource) ([]Orphan, error) {
	return nil, fmt.Errorf("PostgreSQL Reconcile 尚未实现")
}

func connectionDetails(connections []postgresActiveConnection, limit int) string {
	if len(connections) > limit {
		connections = connections[:limit]
	}
	details := make([]string, 0, len(connections))
	for _, item := range connections {
		name := item.application
		if name == "" {
			name = item.user
		}
		if name == "" {
			name = item.state
		}
		if name == "" {
			name = "unknown"
		}
		details = append(details, fmt.Sprintf("%s(pid %d)", name, item.pid))
	}
	return strings.Join(details, ", ")
}

// adminDSN 组装连接维护库或目标库的管理员 DSN。
//
// 参数：database 为空时使用 Extra[maintenance_db]，再回退到 postgres。
// 注意：用户名和密码由 net/url 编码；sslmode 总是显式写入，避免客户端环境隐式改变安全策略。
func adminDSN(ds DataSource, database string) string {
	if database == "" {
		database = ds.Extra["maintenance_db"]
		if database == "" {
			database = "postgres"
		}
	}
	sslmode := ds.Extra["sslmode"]
	if sslmode == "" {
		sslmode = "disable"
	}
	u := &url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(ds.Host, strconv.Itoa(ds.Port)),
		Path:   "/" + database,
		User:   url.UserPassword(ds.User, ds.Password),
	}
	query := url.Values{}
	query.Set("sslmode", sslmode)
	u.RawQuery = query.Encode()
	return u.String()
}

func postgresMajorVersion(version string) (int, error) {
	fields := strings.Fields(version)
	for i, field := range fields {
		if field != "PostgreSQL" || i+1 >= len(fields) {
			continue
		}
		majorText := strings.SplitN(fields[i+1], ".", 2)[0]
		return strconv.Atoi(majorText)
	}
	return 0, fmt.Errorf("版本字符串不含 PostgreSQL 主版本: %q", version)
}
