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

// Plan 由后续任务实现；当前骨架返回未实现错误，避免错误地执行真实动作。
func (p *PostgresProvisioner) Plan(context.Context, DataSource, PlanRequest) (Plan, error) {
	return Plan{}, fmt.Errorf("PostgreSQL Plan 尚未实现")
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
