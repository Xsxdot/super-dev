package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
)

func TestTopoSortRespectsDeps(t *testing.T) {
	nodes := map[string]autostartNode{
		"svc-w": {serviceID: "svc-w", depServiceIDs: []string{"svc-s"}},
		"svc-s": {serviceID: "svc-s"},
	}
	order, cyclic, err := topoSortDeployments(nodes)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(cyclic) != 0 {
		t.Fatalf("unexpected cycle: %v", cyclic)
	}
	if indexOf(order, "svc-s") > indexOf(order, "svc-w") {
		t.Fatalf("server must precede worker: %v", order)
	}
}

func TestTopoSortDetectsCycle(t *testing.T) {
	nodes := map[string]autostartNode{
		"a": {serviceID: "a", depServiceIDs: []string{"b"}},
		"b": {serviceID: "b", depServiceIDs: []string{"a"}},
	}
	_, cyclic, _ := topoSortDeployments(nodes)
	if len(cyclic) == 0 {
		t.Fatal("expected cycle detection")
	}
}

func TestEachAutostartCandidateOnlyReturnsMarkedDeployments(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	app.projects = []model.Project{{
		ID: "proj-a",
		Services: []model.Service{
			{
				ID:   "svc-server",
				Name: "server",
				Deployments: []model.Deployment{
					{ID: "dep-server-dev", EnvName: "dev", StartOnBoot: true},
					{ID: "dep-server-test", EnvName: "test"},
				},
			},
			{
				ID:   "svc-worker",
				Name: "worker",
				Deployments: []model.Deployment{
					{ID: "dep-worker-dev", EnvName: "dev", StartOnBoot: true},
				},
			},
		},
	}}

	var got []string
	app.eachAutostartCandidate(func(projectID, serviceID string, dep model.Deployment) {
		got = append(got, projectID+"/"+serviceID+"/"+dep.ID)
	})

	want := map[string]bool{
		"proj-a/svc-server/dep-server-dev": true,
		"proj-a/svc-worker/dep-worker-dev": true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, key := range got {
		if !want[key] {
			t.Fatalf("unexpected autostart candidate %s in %v", key, got)
		}
	}
}

func TestLookupDeploymentByServiceIDAndEnv(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	app.projects = []model.Project{{
		ID: "proj-a",
		Services: []model.Service{{
			ID:   "svc-server",
			Name: "server",
			Deployments: []model.Deployment{
				{ID: "dep-server-dev", EnvName: "dev"},
				{ID: "dep-server-prod", EnvName: "prod"},
			},
		}},
	}}

	got, ok := app.lookupDeployment("proj-a", "svc-server", "prod")
	if !ok {
		t.Fatal("expected deployment")
	}
	if got.ID != "dep-server-prod" {
		t.Fatalf("got deployment %s", got.ID)
	}
	if _, ok := app.lookupDeployment("proj-a", "svc-server", "missing"); ok {
		t.Fatal("unexpected deployment for missing env")
	}
}

func TestResolveAndWaitDepsUsesRunningDependency(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	serverDep := model.Deployment{
		ID:          "dep-server-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Command:     "sleep 5",
		WorkDir:     t.TempDir(),
	}
	workerDep := model.Deployment{
		ID:          "dep-worker-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		DependsOn:   []string{"svc-server"},
	}
	app.projects = []model.Project{{
		ID: "proj-a",
		Services: []model.Service{
			{ID: "svc-server", Name: "server", Deployments: []model.Deployment{serverDep}},
			{ID: "svc-worker", Name: "worker", Deployments: []model.Deployment{workerDep}},
		},
	}}
	mgr := app.getOrCreateManager("proj-a")
	if err := mgr.StartDeployment(serverDep); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { mgr.StopDeployment(serverDep.ID) })
	waitForDeploymentStatus(t, mgr, serverDep.ID, model.StatusRunning)

	ready := map[string]bool{}
	if err := app.resolveAndWaitDeps("proj-a", workerDep, ready, false); err != nil {
		t.Fatalf("resolve deps: %v", err)
	}
	if !ready["svc-server"] {
		t.Fatal("expected dependency readiness cached")
	}
}

func TestResolveAndWaitDepsErrorsOnMissingDependency(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	app.projects = []model.Project{{ID: "proj-a"}}
	dep := model.Deployment{ID: "dep-worker-dev", EnvName: "dev", DependsOn: []string{"svc-missing"}}

	if err := app.resolveAndWaitDeps("proj-a", dep, map[string]bool{}, false); err == nil {
		t.Fatal("expected missing dependency error")
	}
}

func TestAutostartStartsLocalManagedDeployments(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	serverDep := model.Deployment{
		ID:          "dep-server-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		StartOnBoot: true,
		Command:     "sleep 5",
		WorkDir:     t.TempDir(),
	}
	workerDep := model.Deployment{
		ID:          "dep-worker-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		StartOnBoot: true,
		DependsOn:   []string{"svc-server"},
		Command:     "sleep 5",
		WorkDir:     t.TempDir(),
	}
	remoteDep := model.Deployment{
		ID:          "dep-remote-dev",
		EnvName:     "dev",
		Location:    model.LocationRemote,
		ControlMode: model.ControlModeManaged,
		StartOnBoot: true,
	}
	app.projects = []model.Project{{
		ID: "proj-a",
		Services: []model.Service{
			{ID: "svc-server", Name: "server", Deployments: []model.Deployment{serverDep}},
			{ID: "svc-worker", Name: "worker", Deployments: []model.Deployment{workerDep}},
			{ID: "svc-remote", Name: "remote", Deployments: []model.Deployment{remoteDep}},
		},
	}}
	mgr := app.getOrCreateManager("proj-a")
	t.Cleanup(func() {
		mgr.StopDeployment(serverDep.ID)
		mgr.StopDeployment(workerDep.ID)
	})

	app.runAutostart()

	waitForDeploymentStatus(t, mgr, serverDep.ID, model.StatusRunning)
	waitForDeploymentStatus(t, mgr, workerDep.ID, model.StatusRunning)
	if got := mgr.DeploymentStatus(remoteDep.ID); got != model.StatusStopped {
		t.Fatalf("remote deployment should be skipped, got %q", got)
	}
}

func TestStartAutostartOnceRunsInBackground(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	dep := model.Deployment{
		ID:          "dep-server-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		StartOnBoot: true,
		Command:     "sleep 5",
		WorkDir:     t.TempDir(),
	}
	app.projects = []model.Project{{
		ID:       "proj-a",
		Services: []model.Service{{ID: "svc-server", Name: "server", Deployments: []model.Deployment{dep}}},
	}}
	mgr := app.getOrCreateManager("proj-a")
	t.Cleanup(func() { mgr.StopDeployment(dep.ID) })

	started := time.Now()
	app.startAutostartOnce()
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("startAutostartOnce blocked for %s", elapsed)
	}

	waitForDeploymentStatus(t, mgr, dep.ID, model.StatusRunning)
}

func TestRuntimeStartPlanIncludesCascadeDependencies(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	serverDep := model.Deployment{ID: "dep-server-dev", EnvName: "dev", Location: model.LocationLocal, ControlMode: model.ControlModeManaged}
	workerDep := model.Deployment{ID: "dep-worker-dev", EnvName: "dev", Location: model.LocationLocal, ControlMode: model.ControlModeManaged, DependsOn: []string{"svc-server"}}
	app.projects = []model.Project{{
		ID:           "proj-a",
		Name:         "demo",
		Environments: []model.Environment{{Name: "dev", IsDev: true}},
		Services: []model.Service{
			{ID: "svc-server", Name: "server", Deployments: []model.Deployment{serverDep}},
			{ID: "svc-worker", Name: "worker", Deployments: []model.Deployment{workerDep}},
		},
	}}

	plan, status, msg := app.planOperation(operationTargetRequest{Kind: operation.OperationRuntimeStart, DeploymentID: workerDep.ID})
	if status != 200 {
		t.Fatalf("plan status=%d msg=%s", status, msg)
	}
	var joined strings.Builder
	for _, check := range plan.Checks {
		joined.WriteString(check.Name)
		joined.WriteString(":")
		joined.WriteString(check.Message)
		joined.WriteByte('\n')
	}
	if !strings.Contains(joined.String(), "dep-server-dev") {
		t.Fatalf("plan did not mention cascade dependency: %s", joined.String())
	}
}

func TestStartDeploymentRuntimeCascadesDependencies(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	serverDep := model.Deployment{
		ID:          "dep-server-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Command:     "sleep 5",
		WorkDir:     t.TempDir(),
	}
	workerDep := model.Deployment{
		ID:          "dep-worker-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		DependsOn:   []string{"svc-server"},
		Command:     "sleep 5",
		WorkDir:     t.TempDir(),
	}
	app.projects = []model.Project{{
		ID:   "proj-a",
		Name: "demo",
		Services: []model.Service{
			{ID: "svc-server", Name: "server", Deployments: []model.Deployment{serverDep}},
			{ID: "svc-worker", Name: "worker", Deployments: []model.Deployment{workerDep}},
		},
	}}
	mgr := app.getOrCreateManager("proj-a")
	t.Cleanup(func() {
		mgr.StopDeployment(serverDep.ID)
		mgr.StopDeployment(workerDep.ID)
	})

	if err := app.startDeploymentRuntime(context.Background(), "proj-a", workerDep, intentStartNormal); err != nil {
		t.Fatalf("start worker: %v", err)
	}

	waitForDeploymentStatus(t, mgr, serverDep.ID, model.StatusRunning)
	waitForDeploymentStatus(t, mgr, workerDep.ID, model.StatusRunning)
}

func waitForDeploymentStatus(t *testing.T, mgr interface {
	DeploymentStatus(string) model.ServiceStatus
}, deploymentID string, want model.ServiceStatus) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := mgr.DeploymentStatus(deploymentID); got == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("deployment %s never reached %q, got %q", deploymentID, want, mgr.DeploymentStatus(deploymentID))
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
