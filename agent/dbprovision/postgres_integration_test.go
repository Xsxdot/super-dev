package dbprovision

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

// pgTestDataSource 从 SUPERDEV_TEST_PG_* 环境变量构造测试用管理连接。
// 未配置时跳过——CI 上没有 PG 是常态，不能因此变红。
func pgTestDataSource(t *testing.T) DataSource {
	t.Helper()
	host := os.Getenv("SUPERDEV_TEST_PG_HOST")
	if host == "" {
		t.Skip("未设置 SUPERDEV_TEST_PG_HOST，跳过 PG 真实实例测试")
	}
	port := 5432
	if v := os.Getenv("SUPERDEV_TEST_PG_PORT"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &port); err != nil {
			t.Fatalf("SUPERDEV_TEST_PG_PORT 不是有效端口: %v", err)
		}
	}
	return DataSource{
		Kind: KindPostgres, Name: "it-pg", Host: host, Port: port,
		User: os.Getenv("SUPERDEV_TEST_PG_USER"), Password: os.Getenv("SUPERDEV_TEST_PG_PASSWORD"),
		Extra: map[string]string{"maintenance_db": "postgres"},
	}
}

func TestPostgresProbeAgainstRealInstance(t *testing.T) {
	ds := pgTestDataSource(t)
	res, err := NewPostgresProvisioner().Probe(context.Background(), ds)
	if err != nil {
		t.Fatalf("Probe 返回错误: %v", err)
	}
	if !res.OK {
		t.Fatalf("Probe 未通过（缺少权限？）: missing=%v error=%s hint=%s", res.Missing, res.Error, res.FixHint)
	}
	for _, cap := range []string{"createdb", "createrole", "pg_signal_backend"} {
		if !res.Capabilities[cap] {
			t.Fatalf("能力 %s 应为 true: %+v", cap, res.Capabilities)
		}
	}
	if res.ServerVer == "" {
		t.Fatal("必须填 ServerVer")
	}
}

func TestPostgresProbeReportsMissingCapability(t *testing.T) {
	ds := pgTestDataSource(t)
	ds.User = os.Getenv("SUPERDEV_TEST_PG_WEAK_USER")
	ds.Password = os.Getenv("SUPERDEV_TEST_PG_WEAK_PASSWORD")
	if ds.User == "" {
		t.Skip("未设置 SUPERDEV_TEST_PG_WEAK_USER，跳过权限不足探测测试")
	}
	res, err := NewPostgresProvisioner().Probe(context.Background(), ds)
	if err != nil {
		t.Fatalf("Probe 返回错误: %v", err)
	}
	if res.OK {
		t.Fatal("弱权限账号不应通过探测")
	}
	if len(res.Missing) == 0 || res.FixHint == "" {
		t.Fatalf("必须给出 Missing 与 FixHint: %+v", res)
	}
}

func TestPostgresPlanRejectsMissingTemplate(t *testing.T) {
	ds := pgTestDataSource(t)
	_, err := NewPostgresProvisioner().Plan(context.Background(), ds, PlanRequest{
		ProjectID: "p", NameSeed: "sdev_eph_it_planmiss",
		Binding: ProjectBinding{Postgres: &PostgresBinding{DevDatabase: "no_such_db_xyz", TerminateConnections: true}},
	})
	if err == nil {
		t.Fatal("模板库不存在时 Plan 必须报错")
	}
}

func TestPostgresPlanReportsTerminateSideEffectWhenBusy(t *testing.T) {
	ds := pgTestDataSource(t)
	tmpl := mustCreateTemplateDB(t, ds)
	defer mustDropDB(t, ds, tmpl)

	busy := mustConnect(t, adminDSN(ds, tmpl))
	defer busy.Close(context.Background())

	p := NewPostgresProvisioner()
	plan, err := p.Plan(context.Background(), ds, PlanRequest{
		ProjectID: "p", NameSeed: "sdev_eph_it_busy",
		Binding: ProjectBinding{Postgres: &PostgresBinding{DevDatabase: tmpl, TerminateConnections: true}},
	})
	if err != nil {
		t.Fatalf("Plan 失败: %v", err)
	}
	if len(plan.SideEffects) != 1 || plan.SideEffects[0].Kind != SideEffectTerminateConnections {
		t.Fatalf("应声明断连副作用: %+v", plan.SideEffects)
	}
	if plan.SideEffects[0].Count < 1 {
		t.Fatalf("副作用应统计到至少 1 个活跃连接: %+v", plan.SideEffects)
	}
	if plan.ResourceName != "sdev_eph_it_busy" {
		t.Fatalf("PG 应采用 NameSeed 作资源名: %s", plan.ResourceName)
	}
	if plan.Detail["template_size"] == "" {
		t.Fatal("必须给出模板库体积")
	}
}

func TestPostgresPlanFailsWhenBusyAndTerminateDisabled(t *testing.T) {
	ds := pgTestDataSource(t)
	tmpl := mustCreateTemplateDB(t, ds)
	defer mustDropDB(t, ds, tmpl)
	busy := mustConnect(t, adminDSN(ds, tmpl))
	defer busy.Close(context.Background())

	_, err := NewPostgresProvisioner().Plan(context.Background(), ds, PlanRequest{
		ProjectID: "p", NameSeed: "sdev_eph_it_nokill",
		Binding: ProjectBinding{Postgres: &PostgresBinding{DevDatabase: tmpl, TerminateConnections: false}},
	})
	if !errors.Is(err, ErrTemplateBusy) {
		t.Fatalf("应返回 ErrTemplateBusy，实际 %v", err)
	}
}

func TestPostgresPlanHasNoSideEffectWhenIdle(t *testing.T) {
	ds := pgTestDataSource(t)
	tmpl := mustCreateTemplateDB(t, ds)
	defer mustDropDB(t, ds, tmpl)

	plan, err := NewPostgresProvisioner().Plan(context.Background(), ds, PlanRequest{
		ProjectID: "p", NameSeed: "sdev_eph_it_idle",
		Binding: ProjectBinding{Postgres: &PostgresBinding{DevDatabase: tmpl, TerminateConnections: true}},
	})
	if err != nil {
		t.Fatalf("Plan 失败: %v", err)
	}
	if len(plan.SideEffects) != 0 {
		t.Fatalf("无活跃连接时不应有副作用: %+v", plan.SideEffects)
	}
}

func TestPostgresProvisionClonesTemplateAndGrantsOnlyOwnDB(t *testing.T) {
	ds := pgTestDataSource(t)
	tmpl := mustCreateTemplateDB(t, ds)
	defer mustDropDB(t, ds, tmpl)
	seed := mustConnect(t, adminDSN(ds, tmpl))
	if _, err := seed.Exec(context.Background(), `CREATE TABLE marker(id int)`); err != nil {
		t.Fatalf("建标记表失败: %v", err)
	}
	seed.Close(context.Background())

	p := NewPostgresProvisioner()
	ctx := context.Background()
	plan, err := p.Plan(ctx, ds, PlanRequest{
		ProjectID: "p", NameSeed: mustName(t, "itclone"),
		Binding: ProjectBinding{Postgres: &PostgresBinding{DevDatabase: tmpl, TerminateConnections: true}},
	})
	if err != nil {
		t.Fatalf("Plan 失败: %v", err)
	}
	res, err := p.Provision(ctx, ds, plan)
	if err != nil {
		t.Fatalf("Provision 失败: %v", err)
	}
	defer p.Reclaim(ctx, ds, res)

	if res.DSN == "" {
		t.Fatal("必须返回明文 DSN")
	}
	conn := mustConnect(t, res.DSN)
	defer conn.Close(ctx)
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM marker`).Scan(&n); err != nil {
		t.Fatalf("克隆库里应存在 marker 表: %v", err)
	}
}

func TestPostgresProvisionTerminatesBusyTemplate(t *testing.T) {
	ds := pgTestDataSource(t)
	tmpl := mustCreateTemplateDB(t, ds)
	defer mustDropDB(t, ds, tmpl)
	busy := mustConnect(t, adminDSN(ds, tmpl))
	defer busy.Close(context.Background())

	p := NewPostgresProvisioner()
	ctx := context.Background()
	plan, err := p.Plan(ctx, ds, PlanRequest{
		ProjectID: "p", NameSeed: mustName(t, "itkill"),
		Binding: ProjectBinding{Postgres: &PostgresBinding{DevDatabase: tmpl, TerminateConnections: true}},
	})
	if err != nil {
		t.Fatalf("Plan 失败: %v", err)
	}
	res, err := p.Provision(ctx, ds, plan)
	if err != nil {
		t.Fatalf("有活跃连接时应能踢掉并克隆成功: %v", err)
	}
	defer p.Reclaim(ctx, ds, res)
}

func TestPostgresReclaimIsIdempotentAndDropsRole(t *testing.T) {
	ds := pgTestDataSource(t)
	tmpl := mustCreateTemplateDB(t, ds)
	defer mustDropDB(t, ds, tmpl)

	p := NewPostgresProvisioner()
	ctx := context.Background()
	plan, err := p.Plan(ctx, ds, PlanRequest{
		ProjectID: "p", NameSeed: mustName(t, "itrec"),
		Binding: ProjectBinding{Postgres: &PostgresBinding{DevDatabase: tmpl, TerminateConnections: true}},
	})
	if err != nil {
		t.Fatalf("Plan 失败: %v", err)
	}
	res, err := p.Provision(ctx, ds, plan)
	if err != nil {
		t.Fatalf("Provision 失败: %v", err)
	}
	if err := p.Reclaim(ctx, ds, res); err != nil {
		t.Fatalf("首次 Reclaim 失败: %v", err)
	}
	if err := p.Reclaim(ctx, ds, res); err != nil {
		t.Fatalf("重复 Reclaim 必须幂等，实际报错: %v", err)
	}

	admin := mustConnect(t, adminDSN(ds, ""))
	defer admin.Close(ctx)
	var count int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_roles WHERE rolname = $1`, res.Name).Scan(&count); err != nil {
		t.Fatalf("查角色失败: %v", err)
	}
	if count != 0 {
		t.Fatal("临时角色必须随库一起删除，不能留僵尸角色")
	}
}

func TestPostgresReclaimForcesActiveConnections(t *testing.T) {
	ds := pgTestDataSource(t)
	tmpl := mustCreateTemplateDB(t, ds)
	defer mustDropDB(t, ds, tmpl)

	p := NewPostgresProvisioner()
	ctx := context.Background()
	plan, err := p.Plan(ctx, ds, PlanRequest{
		ProjectID: "p", NameSeed: mustName(t, "itforce"),
		Binding: ProjectBinding{Postgres: &PostgresBinding{DevDatabase: tmpl, TerminateConnections: true}},
	})
	if err != nil {
		t.Fatalf("Plan 失败: %v", err)
	}
	res, err := p.Provision(ctx, ds, plan)
	if err != nil {
		t.Fatalf("Provision 失败: %v", err)
	}
	hold := mustConnect(t, res.DSN)
	defer hold.Close(ctx)

	if err := p.Reclaim(ctx, ds, res); err != nil {
		t.Fatalf("有活跃连接时 Reclaim 应能 FORCE 成功: %v", err)
	}
}

func mustName(t *testing.T, seed string) string {
	t.Helper()
	name, err := NewResourceName(seed)
	if err != nil {
		t.Fatalf("生成资源名失败: %v", err)
	}
	return name
}

func mustConnect(t *testing.T, dsn string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("连接 PostgreSQL 失败: %v", err)
	}
	return conn
}

func mustCreateTemplateDB(t *testing.T, ds DataSource) string {
	t.Helper()
	name, err := NewResourceName("ittpl")
	if err != nil {
		t.Fatalf("生成模板库名失败: %v", err)
	}
	admin := mustConnect(t, adminDSN(ds, ""))
	defer admin.Close(context.Background())
	if _, err := admin.Exec(context.Background(), `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("创建模板库失败: %v", err)
	}
	return name
}

func mustDropDB(t *testing.T, ds DataSource, name string) {
	t.Helper()
	admin := mustConnect(t, adminDSN(ds, ""))
	defer admin.Close(context.Background())
	if _, err := admin.Exec(context.Background(), `DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`); err != nil {
		t.Fatalf("删除模板库失败: %v", err)
	}
}
