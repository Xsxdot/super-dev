// debug_credential_scope_lifecycle_test.go 验证项目配置身份消失时 lease 不会复活。
//
// 职责：
//   - 通过 Agent HTTP interface 覆盖项目删除与 setup/managed 全量替换
//   - 覆盖 config save 快照替换这一内部持久化边界
//   - 证明同 ID 重建不会继承旧 lease，且无关 scope 保持可用
//
// 边界：
//   - 不读取 Store 内部 map，不接触真实凭据
//   - 不启动真实服务、collector 或远端 host
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestDeletingAndReaddingProjectDoesNotRestoreRevokedLease(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)
	firstRoot := writeLeaseLifecycleProject(t, "project-one", []string{"service-one"})
	secondRoot := writeLeaseLifecycleProject(t, "project-two", []string{"service-two"})
	first := addLeaseLifecycleProject(t, srv.URL, firstRoot)
	second := addLeaseLifecycleProject(t, srv.URL, secondRoot)
	createLeaseForScope(t, srv.URL, first.ID, "", "campaign-one", "project-one-secret")
	createLeaseForScope(t, srv.URL, second.ID, "", "campaign-two", "project-two-secret")

	deleteReq, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/projects/"+first.ID, nil)
	require.NoError(t, err)
	deleteResp, err := http.DefaultClient.Do(deleteReq)
	require.NoError(t, err)
	deleteResp.Body.Close()
	require.Equal(t, http.StatusOK, deleteResp.StatusCode)

	rebuilt := addLeaseLifecycleProject(t, srv.URL, firstRoot)
	require.Equal(t, first.ID, rebuilt.ID)
	assert.Empty(t, readLeaseLifecycleCredentials(t, srv.URL, rebuilt.ID, ""))
	assert.Equal(t, []string{"project-two-secret"}, readLeaseLifecycleCredentials(t, srv.URL, second.ID, ""))
}

func TestSetupServiceRemovalAndSameIDRebuildDoesNotRestoreRevokedLease(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)
	root := writeLeaseLifecycleProject(t, "setup-project", []string{"service-one", "service-two"})
	project := addLeaseLifecycleProject(t, srv.URL, root)
	require.Len(t, project.Services, 2)
	firstServiceID := project.Services[0].ID
	secondServiceID := project.Services[1].ID
	createLeaseForScope(t, srv.URL, project.ID, firstServiceID, "campaign-one", "service-one-secret")
	createLeaseForScope(t, srv.URL, project.ID, secondServiceID, "campaign-two", "service-two-secret")

	putLeaseLifecycleSetup(t, srv.URL, project.ID, []model.Service{project.Services[1]})
	putLeaseLifecycleSetup(t, srv.URL, project.ID, project.Services)

	assert.Empty(t, readLeaseLifecycleCredentials(t, srv.URL, project.ID, firstServiceID))
	assert.Equal(t, []string{"service-two-secret"}, readLeaseLifecycleCredentials(t, srv.URL, project.ID, secondServiceID))
}

func TestConfigSaveServiceRemovalAndSameIDRebuildDoesNotRestoreRevokedLease(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)
	root := writeLeaseLifecycleProject(t, "config-project", []string{"service-one", "service-two"})
	project := addLeaseLifecycleProject(t, srv.URL, root)
	require.Len(t, project.Services, 2)
	firstServiceID := project.Services[0].ID
	secondServiceID := project.Services[1].ID
	createLeaseForScope(t, srv.URL, project.ID, firstServiceID, "campaign-one", "service-one-secret")
	createLeaseForScope(t, srv.URL, project.ID, secondServiceID, "campaign-two", "service-two-secret")

	withoutFirst := project
	withoutFirst.Services = append([]model.Service(nil), project.Services[1])
	_, err := app.saveConfigChangeProject(withoutFirst)
	require.NoError(t, err)
	_, err = app.saveConfigChangeProject(project)
	require.NoError(t, err)

	assert.Empty(t, readLeaseLifecycleCredentials(t, srv.URL, project.ID, firstServiceID))
	assert.Equal(t, []string{"service-two-secret"}, readLeaseLifecycleCredentials(t, srv.URL, project.ID, secondServiceID))
}

func TestManagedServiceRemovalAndSameIDRebuildDoesNotRestoreRevokedLease(t *testing.T) {
	app := newManagedDeploymentTestApp(t, t.TempDir())
	srv := newHTTPServerForPackage(t, app)
	desired := []model.ManagedDeployment{
		managedLeaseLifecycleDeployment("managed-service-one", "managed-deployment-one"),
		managedLeaseLifecycleDeployment("managed-service-two", "managed-deployment-two"),
	}
	putLeaseLifecycleManagedDeployments(t, srv.URL, desired)
	createLeaseForScope(t, srv.URL, "managed-project", "managed-service-one", "campaign-one", "managed-one-secret")
	createLeaseForScope(t, srv.URL, "managed-project", "managed-service-two", "campaign-two", "managed-two-secret")

	putLeaseLifecycleManagedDeployments(t, srv.URL, desired[1:])
	putLeaseLifecycleManagedDeployments(t, srv.URL, desired)

	assert.Empty(t, readLeaseLifecycleCredentials(t, srv.URL, "managed-project", "managed-service-one"))
	assert.Equal(t, []string{"managed-two-secret"}, readLeaseLifecycleCredentials(t, srv.URL, "managed-project", "managed-service-two"))
}

func writeLeaseLifecycleProject(t *testing.T, projectID string, serviceIDs []string) string {
	t.Helper()
	root := t.TempDir()
	services := make([]model.Service, 0, len(serviceIDs))
	for _, serviceID := range serviceIDs {
		services = append(services, model.Service{ID: serviceID, ProjectID: projectID, Name: serviceID, Deployments: []model.Deployment{}})
	}
	require.NoError(t, config.NewLoader(root).Save(model.Project{
		ID: projectID, Name: projectID, RootPath: root,
		Environments: []model.Environment{}, Services: services,
	}))
	return root
}

func addLeaseLifecycleProject(t *testing.T, baseURL, root string) model.Project {
	t.Helper()
	body := bytes.NewBufferString(fmt.Sprintf(`{"root_path":%q}`, root))
	resp, err := http.Post(baseURL+"/api/projects", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var project model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&project))
	return project
}

func createLeaseForScope(t *testing.T, baseURL, projectID, serviceID, owner, value string) {
	t.Helper()
	payload := map[string]any{
		"project_id": projectID, "service_id": serviceID, "owner": owner, "ttl_seconds": 3600,
		"credentials": []map[string]string{{"name": "login", "value": value}},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	resp, err := http.Post(baseURL+"/api/debug-credential-leases", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
}

func readLeaseLifecycleCredentials(t *testing.T, baseURL, projectID, serviceID string) []string {
	t.Helper()
	endpoint := baseURL + "/api/debug-credentials?project_id=" + projectID
	if serviceID != "" {
		endpoint += "&service_id=" + serviceID
	}
	resp, err := http.Get(endpoint)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var credentials []model.MergedDebugCredential
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&credentials))
	values := make([]string, 0, len(credentials))
	for _, credential := range credentials {
		values = append(values, credential.Value)
	}
	return values
}

func putLeaseLifecycleSetup(t *testing.T, baseURL, projectID string, services []model.Service) {
	t.Helper()
	entries := make([]map[string]any, 0, len(services))
	for _, service := range services {
		entries = append(entries, map[string]any{
			"id": service.ID, "name": service.Name, "language": service.Language,
			"required": service.Required, "order": service.Order, "deployments": []any{},
		})
	}
	body, err := json.Marshal(map[string]any{"environments": []any{}, "services": entries})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPut, baseURL+"/api/projects/"+projectID+"/setup", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func managedLeaseLifecycleDeployment(serviceID, deploymentID string) model.ManagedDeployment {
	return model.ManagedDeployment{
		ProjectID: "managed-project", ServiceID: serviceID, ServiceName: serviceID,
		DeploymentID: deploymentID, EnvName: "prod", Location: model.LocationRemote,
	}
}

func putLeaseLifecycleManagedDeployments(t *testing.T, baseURL string, deployments []model.ManagedDeployment) {
	t.Helper()
	body, err := json.Marshal(deployments)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPut, baseURL+"/api/managed-deployments", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}
