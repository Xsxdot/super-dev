// handler_datasources_test.go 验证数据源与临时测试资源 HTTP 契约。
//
// 职责：覆盖脱敏、探测失败提示、活跃租约保护、回收、对账与 dry-run。
// 边界：只使用 fake Provisioner 和临时 SQLite/JSON，不连接真实 PG/Redis。
package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/api"
	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/dbprovision"
	"github.com/xsxdot/super-dev/agent/store"
)

type httpFakeProvisioner struct {
	kind      string
	probe     dbprovision.ProbeResult
	reclaimed *int
}

func (p *httpFakeProvisioner) Kind() string { return p.kind }

func (p *httpFakeProvisioner) Probe(context.Context, dbprovision.DataSource) (dbprovision.ProbeResult, error) {
	return p.probe, nil
}

func (p *httpFakeProvisioner) Plan(_ context.Context, _ dbprovision.DataSource, req dbprovision.PlanRequest) (dbprovision.Plan, error) {
	return dbprovision.Plan{Kind: p.kind, ResourceName: req.NameSeed, Steps: []string{"fake provision"}}, nil
}

func (p *httpFakeProvisioner) Provision(_ context.Context, ds dbprovision.DataSource, plan dbprovision.Plan) (dbprovision.Resource, error) {
	return dbprovision.Resource{Kind: p.kind, Name: plan.ResourceName, DSN: "fake://user:secret@" + ds.Host + "/db"}, nil
}

func (p *httpFakeProvisioner) Reclaim(context.Context, dbprovision.DataSource, dbprovision.Resource) error {
	if p.reclaimed != nil {
		*p.reclaimed++
	}
	return nil
}

func (p *httpFakeProvisioner) Reconcile(context.Context, dbprovision.DataSource, []dbprovision.Resource) ([]dbprovision.Orphan, error) {
	return nil, nil
}

func writeDataSources(t *testing.T, dataDir string, sources []dbprovision.DataSource) {
	t.Helper()
	data, err := json.Marshal(sources)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "datasources.json"), data, 0o600))
}

func registerHTTPProvisioner(t *testing.T, provisioner dbprovision.Provisioner) {
	t.Helper()
	previous, existed := dbprovision.LookupProvisioner(provisioner.Kind())
	dbprovision.RegisterProvisioner(provisioner)
	t.Cleanup(func() {
		if existed {
			dbprovision.RegisterProvisioner(previous)
		}
	})
}

func seedActiveLease(t *testing.T, dataDir, leaseID, projectID, datasourceID, kind, name, datasourceName string) {
	t.Helper()
	s, err := store.New(filepath.Join(dataDir, "logs.db"))
	require.NoError(t, err)
	now := time.Now().UTC()
	lease := dbprovision.Lease{ID: leaseID, ProjectID: projectID, Purpose: "HTTP test", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	require.NoError(t, s.InsertLease(lease))
	resourceID, err := s.InsertResource(leaseID, datasourceID, dbprovision.Resource{
		Kind: kind,
		Name: name,
		Meta: map[string]string{"datasource_name": datasourceName},
	})
	require.NoError(t, err)
	require.NoError(t, s.MarkResourceActive(resourceID))
	require.NoError(t, s.Close())
}

func requestBody(t *testing.T, app *api.App, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	return rec
}

func TestListDataSourcesRedactsPassword(t *testing.T) {
	dataDir := t.TempDir()
	writeDataSources(t, dataDir, []dbprovision.DataSource{{
		ID: "ds-pg", Kind: dbprovision.KindPostgres, Name: "local-pg", Host: "127.0.0.1", Port: 5432,
		Password: "super-secret",
	}})
	app, err := api.NewApp(api.AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	rec := requestBody(t, app, http.MethodGet, "/api/datasources", "")
	require.Equal(t, http.StatusOK, rec.Code)
	body, err := io.ReadAll(rec.Result().Body)
	require.NoError(t, err)
	require.NotContains(t, string(body), "super-secret")
}

func TestCreateDataSourceReturnsProbeFixHintOn4xx(t *testing.T) {
	kind := "api-probe-fake"
	registerHTTPProvisioner(t, &httpFakeProvisioner{kind: kind, probe: dbprovision.ProbeResult{
		OK: false, Missing: []string{"createdb"}, FixHint: "ALTER ROLE test CREATEDB;", Error: "permission denied",
	}})
	dataDir := t.TempDir()
	app, err := api.NewApp(api.AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	rec := requestBody(t, app, http.MethodPost, "/api/datasources", `{"kind":"api-probe-fake","name":"bad","host":"127.0.0.1","port":1234}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	body, err := io.ReadAll(rec.Result().Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "createdb")
	require.Contains(t, string(body), "fix_hint")
}

func TestDeleteDataSourceConflictsWhenLeasesActive(t *testing.T) {
	kind := "api-delete-fake"
	registerHTTPProvisioner(t, &httpFakeProvisioner{kind: kind, probe: dbprovision.ProbeResult{OK: true}})
	dataDir := t.TempDir()
	writeDataSources(t, dataDir, []dbprovision.DataSource{{ID: "ds-active", Kind: kind, Name: "active", Host: "127.0.0.1", Port: 1}})
	seedActiveLease(t, dataDir, "lease-active", "project", "ds-active", kind, "resource", "active")
	app, err := api.NewApp(api.AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	rec := requestBody(t, app, http.MethodDelete, "/api/datasources/ds-active", "")
	require.Equal(t, http.StatusConflict, rec.Code)
	rec = requestBody(t, app, http.MethodDelete, "/api/datasources/ds-active?force=true", "")
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestDeleteTestDatabaseReclaims(t *testing.T) {
	reclaimed := 0
	registerHTTPProvisioner(t, &httpFakeProvisioner{kind: dbprovision.KindPostgres, probe: dbprovision.ProbeResult{OK: true}, reclaimed: &reclaimed})
	dataDir := t.TempDir()
	writeDataSources(t, dataDir, []dbprovision.DataSource{{ID: "ds-pg", Kind: dbprovision.KindPostgres, Name: "local-pg", Host: "127.0.0.1", Port: 5432}})
	seedActiveLease(t, dataDir, "lease-reclaim", "project", "ds-pg", dbprovision.KindPostgres, "resource", "local-pg")
	app, err := api.NewApp(api.AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	rec := requestBody(t, app, http.MethodDelete, "/api/test-databases/lease-reclaim", "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, 1, reclaimed)
	list := requestBody(t, app, http.MethodGet, "/api/test-databases", "")
	require.Equal(t, http.StatusOK, list.Code)
	require.NotContains(t, list.Body.String(), "lease-reclaim")
}

func TestListTestDatabasesOmitsDSN(t *testing.T) {
	dataDir := t.TempDir()
	app, err := api.NewApp(api.AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	rec := requestBody(t, app, http.MethodGet, "/api/test-databases", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "dsn")
}

func TestReconcileEndpointReturnsReport(t *testing.T) {
	app, err := api.NewApp(api.AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	rec := requestBody(t, app, http.MethodPost, "/api/test-databases/reconcile", "{}")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "expired_reclaimed")
}

func TestDryRunMasksDSN(t *testing.T) {
	registerHTTPProvisioner(t, &httpFakeProvisioner{kind: dbprovision.KindPostgres, probe: dbprovision.ProbeResult{OK: true}})
	dataDir := t.TempDir()
	projectRoot := t.TempDir()
	projectYAML := `name: dry-run-project
id: project-dry
services: []
data_source_binding:
  postgres:
    datasource_name: local-pg
    dev_database: tk_dev
`
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, ".superdev"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, ".superdev", "project.yaml"), []byte(projectYAML), 0o644))
	require.NoError(t, config.NewRegistry(filepath.Join(dataDir, "projects.json")).Add(projectRoot))
	writeDataSources(t, dataDir, []dbprovision.DataSource{{ID: "ds-pg", Kind: dbprovision.KindPostgres, Name: "local-pg", Host: "127.0.0.1", Port: 5432}})
	app, err := api.NewApp(api.AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	rec := requestBody(t, app, http.MethodPost, "/api/projects/project-dry/test-database/dry-run", `{}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "***")
	require.NotContains(t, rec.Body.String(), "secret@")
}
