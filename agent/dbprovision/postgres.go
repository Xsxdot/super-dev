// postgres.go —— PostgreSQL 临时数据库供给器的管理连接探测实现。
//
// 职责：使用管理连接探测 PostgreSQL 版本、建库/建角色/断连能力，并为后续 Plan/Provision
// 提供安全的管理员 DSN 拼装能力。
// 边界：不管理租约、配额或项目绑定；资源生命周期动作将在后续方法中实现，明文凭据不写日志。
package dbprovision

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// Provision 按已审批的 Plan 创建临时角色与模板克隆库，并在失败时级联回滚。
//
// 返回：含明文 DSN 的 Resource；该明文只应由 acquire_test_database 作为一次性出口返回。
// 注意：CREATE DATABASE 不能在事务中执行，因此本方法用 defer 手工删除已完成的中间产物。
func (p *PostgresProvisioner) Provision(ctx context.Context, ds DataSource, plan Plan) (Resource, error) {
	resourceName := plan.ResourceName
	template := plan.Detail["template"]
	if resourceName == "" || template == "" {
		return Resource{}, ErrBindingMissing
	}
	willTerminate := hasSideEffect(plan, SideEffectTerminateConnections)
	log := logger.GetLogger().WithEntryName("DBProvisionPG").WithFields(map[string]any{
		"resource_name": resourceName, "template": template, "will_terminate": willTerminate,
	})
	log.Info("开始供给 PostgreSQL 临时库")
	started := time.Now()
	passwordBytes := make([]byte, 24)
	if _, err := rand.Read(passwordBytes); err != nil {
		log.WithField("step", "generate_password").WithErr(err).Error("生成 PostgreSQL 临时角色密码失败")
		return Resource{}, fmt.Errorf("生成临时角色密码失败: %w", err)
	}
	password := base64.RawURLEncoding.EncodeToString(passwordBytes)
	conn, err := pgx.Connect(ctx, adminDSN(ds, ""))
	if err != nil {
		log.WithField("step", "connect_admin").WithErr(err).Error("连接 PostgreSQL 维护库失败")
		return Resource{}, fmt.Errorf("连接 PostgreSQL 维护库失败: %w", err)
	}
	defer conn.Close(ctx)

	roleCreated := false
	databaseCreated := false
	rollback := true
	defer func() {
		if !rollback {
			return
		}
		rolledBackDB := false
		rolledBackRole := false
		if databaseCreated {
			if _, dropErr := conn.Exec(ctx, `DROP DATABASE IF EXISTS `+pgx.Identifier{resourceName}.Sanitize()+` WITH (FORCE)`); dropErr == nil {
				rolledBackDB = true
			} else {
				log.WithField("step", "rollback_database").WithErr(dropErr).Error("回滚 PostgreSQL 临时库失败")
			}
		}
		if roleCreated {
			if _, dropErr := conn.Exec(ctx, `DROP ROLE IF EXISTS `+pgx.Identifier{resourceName}.Sanitize()); dropErr == nil {
				rolledBackRole = true
			} else {
				log.WithField("step", "rollback_role").WithErr(dropErr).Error("回滚 PostgreSQL 临时角色失败")
			}
		}
		log.WithFields(map[string]any{
			"resource_name": resourceName, "rolled_back_db": rolledBackDB, "rolled_back_role": rolledBackRole,
		}).Warn("PostgreSQL 临时库供给失败，已触发回滚")
	}()

	log.WithField("role", resourceName).Debug("开始创建 PostgreSQL 临时角色")
	createRole := fmt.Sprintf(
		`CREATE ROLE %s LOGIN PASSWORD %s NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT`,
		pgx.Identifier{resourceName}.Sanitize(), quoteLiteral(password),
	)
	if _, err := conn.Exec(ctx, createRole); err != nil {
		log.WithFields(map[string]any{"resource_name": resourceName, "step": "create_role"}).WithErr(err).Error("创建 PostgreSQL 临时角色失败")
		return Resource{}, fmt.Errorf("创建临时角色失败: %w", err)
	}
	roleCreated = true
	log.WithField("role", resourceName).Debug("PostgreSQL 临时角色创建完成")

	if willTerminate {
		if _, err := terminateTemplateConnections(ctx, conn, template); err != nil {
			log.WithFields(map[string]any{"resource_name": resourceName, "step": "terminate_connections"}).WithErr(err).Error("断开 PostgreSQL 模板库连接失败")
			return Resource{}, fmt.Errorf("断开模板库连接失败: %w", err)
		}
		// pg_terminate_backend 返回并不代表后端已退出，等待 200ms 再发起克隆可避开短暂竞态。
		time.Sleep(200 * time.Millisecond)
	}

	createDatabase := func() error {
		log.WithFields(map[string]any{"resource_name": resourceName, "template": template}).Info("开始创建 PostgreSQL 临时库")
		_, createErr := conn.Exec(ctx, fmt.Sprintf(
			`CREATE DATABASE %s TEMPLATE %s OWNER %s`,
			pgx.Identifier{resourceName}.Sanitize(), pgx.Identifier{template}.Sanitize(), pgx.Identifier{resourceName}.Sanitize(),
		))
		return createErr
	}
	if err := createDatabase(); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55006" && willTerminate {
			// 只重试一次，避免在开发库持续瞬断时无限延长本次申请的影响窗口。
			log.WithFields(map[string]any{"resource_name": resourceName, "attempt": 2}).WithErr(err).Warn("模板库仍繁忙，断连后重试创建临时库")
			if _, terminateErr := terminateTemplateConnections(ctx, conn, template); terminateErr != nil {
				log.WithFields(map[string]any{"resource_name": resourceName, "step": "retry_terminate_connections"}).WithErr(terminateErr).Error("重试断开 PostgreSQL 模板库连接失败")
				return Resource{}, fmt.Errorf("重试断开模板库连接失败: %w", terminateErr)
			}
			// pg_terminate_backend 返回并不代表后端已经退出，等待后再重试可避免立即再次收到 55006。
			time.Sleep(200 * time.Millisecond)
			if err := createDatabase(); err != nil {
				log.WithFields(map[string]any{"resource_name": resourceName, "step": "create_database_retry"}).WithErr(err).Error("重试创建 PostgreSQL 临时库失败")
				return Resource{}, fmt.Errorf("重试创建临时库失败: %w", err)
			}
		} else {
			log.WithFields(map[string]any{"resource_name": resourceName, "step": "create_database"}).WithErr(err).Error("创建 PostgreSQL 临时库失败")
			return Resource{}, fmt.Errorf("创建临时库失败: %w", err)
		}
	}
	databaseCreated = true
	log.WithFields(map[string]any{
		"resource_name": resourceName, "elapsed_ms": time.Since(started).Milliseconds(),
	}).Info("PostgreSQL 临时库创建完成")

	resDSN := postgresDSN(ds, resourceName, password, resourceName)
	// CREATE DATABASE 不能包在事务里；库建好后再连入目标库执行 REVOKE CONNECT，才能收紧 PUBLIC 权限。
	targetConn, err := pgx.Connect(ctx, resDSN)
	if err != nil {
		log.WithFields(map[string]any{"resource_name": resourceName, "step": "connect_target"}).WithErr(err).Error("连接 PostgreSQL 临时库失败")
		return Resource{}, fmt.Errorf("连接临时库失败: %w", err)
	}
	_, revokeErr := targetConn.Exec(ctx, `REVOKE CONNECT ON DATABASE `+pgx.Identifier{resourceName}.Sanitize()+` FROM PUBLIC`)
	_ = targetConn.Close(ctx)
	if revokeErr != nil {
		log.WithFields(map[string]any{"resource_name": resourceName, "step": "revoke_public_connect"}).WithErr(revokeErr).Error("收紧 PostgreSQL 临时库连接权限失败")
		return Resource{}, fmt.Errorf("收紧临时库连接权限失败: %w", revokeErr)
	}
	rollback = false
	log.WithFields(map[string]any{
		"resource_name": resourceName, "elapsed_ms": time.Since(started).Milliseconds(),
	}).Info("PostgreSQL 临时库供给成功")
	return Resource{
		Kind: KindPostgres,
		Name: resourceName,
		DSN:  resDSN,
		Meta: map[string]string{"database": resourceName, "role": resourceName, "cloned_from": template},
	}, nil
}

// Reclaim 强制删除 PostgreSQL 临时库与其同名角色，重复调用幂等。
//
// 注意：必须先删库再删角色，因为角色是数据库 owner，反过来会因 owner 依赖而失败。
func (p *PostgresProvisioner) Reclaim(ctx context.Context, ds DataSource, res Resource) error {
	log := logger.GetLogger().WithEntryName("DBProvisionPG").WithField("resource_name", res.Name)
	log.Info("开始回收 PostgreSQL 临时库")
	conn, err := pgx.Connect(ctx, adminDSN(ds, ""))
	if err != nil {
		log.WithErr(err).Error("连接 PostgreSQL 维护库失败")
		return fmt.Errorf("连接 PostgreSQL 维护库失败: %w", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `DROP DATABASE IF EXISTS `+pgx.Identifier{res.Name}.Sanitize()+` WITH (FORCE)`); err != nil {
		log.WithErr(err).Error("回收 PostgreSQL 临时库失败")
		return fmt.Errorf("删除临时库失败: %w", err)
	}
	role := res.Meta["role"]
	if role == "" {
		role = res.Name
	}
	if _, err := conn.Exec(ctx, `DROP ROLE IF EXISTS `+pgx.Identifier{role}.Sanitize()); err != nil {
		log.WithErr(err).Error("回收 PostgreSQL 临时角色失败")
		return fmt.Errorf("删除临时角色失败: %w", err)
	}
	log.Info("PostgreSQL 临时库回收完成")
	return nil
}

// Reconcile 扫描 PostgreSQL 中带 ResourcePrefix 的数据库与角色孤儿。
//
// 注意：只报告带 ResourcePrefix 前缀且不在 known 中的资源，无法确证归属的一律放过；
// 本方法只报告，不执行回收，实际回收由上层按同一 Resource 语义调用 Reclaim。
func (p *PostgresProvisioner) Reconcile(ctx context.Context, ds DataSource, known []Resource) ([]Orphan, error) {
	log := logger.GetLogger().WithEntryName("DBProvisionPG").WithField("known_count", len(known))
	log.Debug("开始对账 PostgreSQL 临时资源")
	knownNames := make(map[string]struct{}, len(known))
	for _, resource := range known {
		knownNames[resource.Name] = struct{}{}
	}
	conn, err := pgx.Connect(ctx, adminDSN(ds, ""))
	if err != nil {
		log.WithErr(err).Error("连接 PostgreSQL 维护库进行对账失败")
		return nil, fmt.Errorf("连接 PostgreSQL 维护库失败: %w", err)
	}
	defer conn.Close(ctx)

	// LIKE 中的下划线是通配符，必须用 ESCAPE 转义，否则会把 sdevXephY... 等无关库名也扫进来。
	rows, err := conn.Query(ctx, `
		SELECT datname FROM pg_database
		WHERE datname LIKE 'sdev\_eph\_%' ESCAPE '\'
		ORDER BY datname
	`)
	if err != nil {
		log.WithErr(err).Error("查询 PostgreSQL 临时数据库对账列表失败")
		return nil, fmt.Errorf("查询临时数据库失败: %w", err)
	}
	var orphans []Orphan
	orphanNames := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			log.WithErr(err).Error("读取 PostgreSQL 临时数据库对账项失败")
			return nil, fmt.Errorf("读取临时数据库失败: %w", err)
		}
		if _, ok := knownNames[name]; ok {
			continue
		}
		orphanNames[name] = struct{}{}
		orphans = append(orphans, Orphan{Kind: KindPostgres, Name: name, Reason: "库带 sdev_eph_ 前缀但不在登记表中"})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		log.WithErr(err).Error("遍历 PostgreSQL 临时数据库对账项失败")
		return nil, fmt.Errorf("遍历临时数据库失败: %w", err)
	}
	rows.Close()

	rows, err = conn.Query(ctx, `
		SELECT rolname FROM pg_roles
		WHERE rolname LIKE 'sdev\_eph\_%' ESCAPE '\'
		ORDER BY rolname
	`)
	if err != nil {
		log.WithErr(err).Error("查询 PostgreSQL 临时角色对账列表失败")
		return nil, fmt.Errorf("查询临时角色失败: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			log.WithErr(err).Error("读取 PostgreSQL 临时角色对账项失败")
			return nil, fmt.Errorf("读取临时角色失败: %w", err)
		}
		if _, ok := knownNames[name]; ok {
			continue
		}
		if _, ok := orphanNames[name]; ok {
			continue
		}
		orphans = append(orphans, Orphan{Kind: KindPostgres, Name: name, Reason: "角色带 sdev_eph_ 前缀但无对应登记"})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		log.WithErr(err).Error("遍历 PostgreSQL 临时角色对账项失败")
		return nil, fmt.Errorf("遍历临时角色失败: %w", err)
	}
	rows.Close()
	if len(orphans) > 0 {
		names := make([]string, 0, len(orphans))
		for _, orphan := range orphans {
			names = append(names, orphan.Name)
		}
		log.WithFields(map[string]any{"orphan_count": len(orphans), "names": names}).Warn("发现 PostgreSQL 临时资源孤儿")
	} else {
		log.WithField("scanned", true).Debug("PostgreSQL 临时资源对账未发现孤儿")
	}
	return orphans, nil
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
	return postgresDSN(ds, ds.User, ds.Password, database)
}

func postgresDSN(ds DataSource, user, password, database string) string {
	sslmode := ds.Extra["sslmode"]
	if sslmode == "" {
		sslmode = "disable"
	}
	u := &url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(ds.Host, strconv.Itoa(ds.Port)),
		Path:   "/" + database,
		User:   url.UserPassword(user, password),
	}
	query := url.Values{}
	query.Set("sslmode", sslmode)
	u.RawQuery = query.Encode()
	return u.String()
}

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func hasSideEffect(plan Plan, kind string) bool {
	for _, effect := range plan.SideEffects {
		if effect.Kind == kind {
			return true
		}
	}
	return false
}

func terminateTemplateConnections(ctx context.Context, conn *pgx.Conn, database string) (int, error) {
	rows, err := conn.Query(ctx, `
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE datname = $1 AND pid <> pg_backend_pid()
	`, database)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	terminated := 0
	for rows.Next() {
		var ok bool
		if err := rows.Scan(&ok); err != nil {
			return terminated, err
		}
		if ok {
			terminated++
		}
	}
	if err := rows.Err(); err != nil {
		return terminated, err
	}
	logger.GetLogger().WithEntryName("DBProvisionPG").WithFields(map[string]any{
		"template": database, "terminated": terminated,
	}).Info("已断开 PostgreSQL 模板库连接")
	return terminated, nil
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
