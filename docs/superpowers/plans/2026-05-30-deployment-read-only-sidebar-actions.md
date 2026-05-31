# Deployment Read-Only Sidebar Actions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add explicit per-deployment `read_only` control and sidebar row hover actions for single-deployment start, restart, and stop.

**Architecture:** Keep lifecycle authority in the existing deployment-level Go API. Persist `read_only` through `.superdev/config.yaml`, expose it through the agent JSON model, and let the Vue sidebar render controls from the current deployment state without adding service-level APIs.

**Tech Stack:** Go agent (`model`, `config`, `api`), Vue 3 + Pinia + Vitest desktop client, existing CSS scoped components.

---

## File Map

- Modify `agent/model/model.go`: add `Deployment.ReadOnly`, change `IsReadOnly()` to explicit-field semantics.
- Modify `agent/model/model_test.go`: replace old derived read-only tests with explicit field tests.
- Modify `agent/config/loader.go`: map YAML `read_only` to and from `model.Deployment`.
- Modify `agent/config/loader_test.go`: cover read and Save/Load preservation of `read_only`.
- Modify `agent/api/handler_deployments.go`: guard start, stop, and restart with explicit read-only checks.
- Modify `agent/api/api_test.go`: cover read-only API rejection and non-read-only remote deployments without commands.
- Modify `desktop/src/api/agent.ts`: add `read_only` to `Deployment` and `SetupDeployment`.
- Modify `desktop/src/lib/configDraft.ts`: preserve `read_only` in setup payload.
- Modify `desktop/src/lib/__tests__/configDraft.test.ts`: verify payload preserves `read_only`.
- Modify `desktop/src/components/Settings/DeploymentForm.vue`: add the read-only toggle.
- Create `desktop/src/components/Settings/__tests__/DeploymentForm.test.ts`: verify toggle emits updated deployment.
- Modify `desktop/src/components/Sidebar/EnvGroup.vue`: add row-level hover actions and read-only hiding.
- Modify `desktop/src/components/Sidebar/__tests__/EnvGroup.test.ts`: verify action visibility and click behavior.

## Task 1: Go Model Explicit Read-Only

**Files:**
- Modify: `agent/model/model.go`
- Modify: `agent/model/model_test.go`

- [x] **Step 1: Write failing model tests**

Replace the current read-only tests in `agent/model/model_test.go` with these tests:

```go
func TestDeploymentReadOnlyUsesExplicitField(t *testing.T) {
	d := model.Deployment{Location: model.LocationLocal, ReadOnly: true}
	assert.True(t, d.IsReadOnly())

	d = model.Deployment{Location: model.LocationRemote, ReadOnly: true}
	assert.True(t, d.IsReadOnly())
}

func TestDeploymentNotReadOnlyByDefault(t *testing.T) {
	d := model.Deployment{Location: model.LocationRemote}
	assert.False(t, d.IsReadOnly())
}

func TestDeploymentCommandPresenceDoesNotControlReadOnly(t *testing.T) {
	withoutCommands := model.Deployment{Location: model.LocationRemote}
	assert.False(t, withoutCommands.IsReadOnly())

	withCommands := model.Deployment{
		Location:     model.LocationRemote,
		StartCommand: "sudo systemctl start api",
		StopCommand:  "sudo systemctl stop api",
	}
	assert.False(t, withCommands.IsReadOnly())
}
```

- [x] **Step 2: Run model tests and confirm failure**

Run:

```bash
go test ./model -run 'TestDeployment(ReadOnlyUsesExplicitField|NotReadOnlyByDefault|CommandPresenceDoesNotControlReadOnly)' -count=1
```

Expected: failure because `Deployment.ReadOnly` does not exist and `IsReadOnly()` still derives from command fields.

- [x] **Step 3: Implement explicit field**

Update `agent/model/model.go`:

```go
	// ReadOnly 为 true 时该 deployment 只能查看日志，不能被启动、停止或重启。
	ReadOnly bool `json:"read_only,omitempty" yaml:"read_only,omitempty"`

	// 远程可选启停命令；是否允许启停由 ReadOnly 显式控制。
	StartCommand string `json:"start_command,omitempty"`
	StopCommand  string `json:"stop_command,omitempty"`
```

Update the `Deployment` comment so the control bullet says:

```go
//   - 描述「能不能控制」（ReadOnly 为 true 时只能查看日志）
```

Update `IsReadOnly()`:

```go
// IsReadOnly 报告该 deployment 是否只能查看日志、不能启停。
//
// read_only 是显式能力开关。命令是否存在不参与只读判断，
// 便于未来通过 sudo、远程控制代理等方式补齐启停能力。
func (d Deployment) IsReadOnly() bool {
	return d.ReadOnly
}
```

- [x] **Step 4: Run model tests and confirm pass**

Run:

```bash
go test ./model -count=1
```

Expected: PASS.

## Task 2: Config YAML Read/Write

**Files:**
- Modify: `agent/config/loader.go`
- Modify: `agent/config/loader_test.go`

- [x] **Step 1: Write failing config tests**

Change `TestLoadNewFormatReadOnlyDeployment` in `agent/config/loader_test.go` so the YAML contains `read_only: true` and the assertion reads `assert.True(t, prod.ReadOnly)`.

Add this assertion to the existing `prod` block in `TestSaveAndReloadWithEnvironmentsAndDeployments` after `StopCommand` is asserted:

```go
assert.True(t, prod.ReadOnly)
```

Set the source deployment in that same test to:

```go
ReadOnly: true,
```

- [x] **Step 2: Run config tests and confirm failure**

Run:

```bash
go test ./config -run 'TestLoadNewFormatReadOnlyDeployment|TestSaveAndReloadWithEnvironmentsAndDeployments' -count=1
```

Expected: failure because YAML mapping does not yet include `read_only`.

- [x] **Step 3: Implement loader mapping**

In `agent/config/loader.go`, add to `deploymentYAML`:

```go
ReadOnly bool `yaml:"read_only,omitempty"`
```

Add to `deploymentsFromYAML`:

```go
ReadOnly:     d.ReadOnly,
```

Add to `deploymentsToYAML`:

```go
ReadOnly:     d.ReadOnly,
```

- [x] **Step 4: Run config tests and confirm pass**

Run:

```bash
go test ./config -count=1
```

Expected: PASS.

## Task 3: Deployment API Guards

**Files:**
- Modify: `agent/api/handler_deployments.go`
- Modify: `agent/api/api_test.go`

- [x] **Step 1: Write failing API tests**

Add a helper in `agent/api/api_test.go` near `TestDeploymentStartStop`:

```go
func addProjectFromConfig(t *testing.T, srvURL string, cfg string) model.Project {
	t.Helper()
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".superdev")
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(cfg), 0o644))

	body, _ := json.Marshal(map[string]string{"root_path": dir})
	resp, err := http.Post(srvURL+"/api/projects", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	defer resp.Body.Close()

	var project model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&project))
	return project
}
```

Add this test:

```go
func TestReadOnlyDeploymentRejectsLifecycleControls(t *testing.T) {
	srv, _ := newTestApp(t)
	project := addProjectFromConfig(t, srv.URL, `
name: myproject
environments:
  - name: dev
    is_dev: true
    order: 0
services:
  - name: api
    required: false
    order: 0
    deployments:
      - env: dev
        location: local
        command: "sleep 60"
        working_dir: "."
        read_only: true
`)
	depID := project.Services[0].Deployments[0].ID

	for _, action := range []string{"start", "stop", "restart"} {
		resp, err := http.Post(srv.URL+"/api/deployments/"+depID+"/"+action, "application/json", nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, action)
		_ = resp.Body.Close()
	}
}
```

Add this test:

```go
func TestRemoteDeploymentWithoutCommandsIsNotRejectedAsReadOnly(t *testing.T) {
	srv, _ := newTestApp(t)
	project := addProjectFromConfig(t, srv.URL, `
name: myproject
environments:
  - name: prod
    is_dev: false
    order: 0
services:
  - name: api
    required: false
    order: 0
    deployments:
      - env: prod
        location: remote
        hosts: [prod-01]
        log_type: journalctl
        log_target: api.service
`)
	depID := project.Services[0].Deployments[0].ID

	resp, err := http.Post(srv.URL+"/api/deployments/"+depID+"/start", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.NotEqual(t, http.StatusBadRequest, resp.StatusCode)
}
```

- [x] **Step 2: Run API tests and confirm failure**

Run:

```bash
go test ./api -run 'TestReadOnlyDeploymentRejectsLifecycleControls|TestRemoteDeploymentWithoutCommandsIsNotRejectedAsReadOnly' -count=1
```

Expected: failure because `stop` has no read-only guard and remote deployments without commands are still considered read-only until Task 1 is implemented.

- [x] **Step 3: Implement API guard**

In `agent/api/handler_deployments.go`, update the file header bullet:

```go
//   - remote/local deployment：IsReadOnly() 为 true 时返回 400
```

In `startDeployment` and `restartDeployment`, change the error to:

```go
jsonError(w, http.StatusBadRequest, "deployment is read-only")
```

In `stopDeployment`, add this after the not-found guard:

```go
if dep.IsReadOnly() {
	jsonError(w, http.StatusBadRequest, "deployment is read-only")
	return
}
```

- [x] **Step 4: Run API tests and confirm pass**

Run:

```bash
go test ./api -run 'TestDeploymentStartStop|TestReadOnlyDeploymentRejectsLifecycleControls|TestRemoteDeploymentWithoutCommandsIsNotRejectedAsReadOnly' -count=1
```

Expected: PASS.

## Task 4: Frontend Types And Config Draft

**Files:**
- Modify: `desktop/src/api/agent.ts`
- Modify: `desktop/src/lib/configDraft.ts`
- Modify: `desktop/src/lib/__tests__/configDraft.test.ts`

- [x] **Step 1: Write failing draft test**

Add to `desktop/src/lib/__tests__/configDraft.test.ts`:

```ts
it('draftToPayload 透传 read_only', () => {
  const draft = projectToDraft(makeProject())
  draft.services[0]!.deployments[0]!.read_only = true
  const payload = draftToPayload(draft)
  expect(payload.services[0]!.deployments[0]!.read_only).toBe(true)
})
```

- [x] **Step 2: Run draft test and confirm failure**

Run:

```bash
cd desktop && pnpm test src/lib/__tests__/configDraft.test.ts
```

Expected: TypeScript/Vitest failure because `read_only` is not typed or not included in payload.

- [x] **Step 3: Add frontend types and payload mapping**

In `desktop/src/api/agent.ts`, add to both `Deployment` and `SetupDeployment`:

```ts
  read_only?: boolean
```

In `desktop/src/lib/configDraft.ts`, add to the deployment payload:

```ts
        read_only: d.read_only,
```

- [x] **Step 4: Run draft test and confirm pass**

Run:

```bash
cd desktop && pnpm test src/lib/__tests__/configDraft.test.ts
```

Expected: PASS.

## Task 5: Deployment Form Read-Only Toggle

**Files:**
- Modify: `desktop/src/components/Settings/DeploymentForm.vue`
- Create: `desktop/src/components/Settings/__tests__/DeploymentForm.test.ts`

- [x] **Step 1: Write failing component test**

Create `desktop/src/components/Settings/__tests__/DeploymentForm.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import DeploymentForm from '@/components/Settings/DeploymentForm.vue'
import type { Deployment } from '@/api/agent'

function dep(extra: Partial<Deployment> = {}): Deployment {
  return {
    id: 'd1',
    env_name: 'dev',
    location: 'local',
    command: 'go run .',
    work_dir: '/tmp/demo',
    status: '',
    ...extra,
  }
}

describe('DeploymentForm', () => {
  it('切换只读开关时 emit read_only 更新', async () => {
    const wrapper = mount(DeploymentForm, {
      props: { modelValue: dep(), hosts: [] },
    })

    await wrapper.find('[data-test="dep-read-only"]').setValue(true)

    const emitted = wrapper.emitted('update:modelValue')
    expect(emitted).toBeTruthy()
    expect((emitted![0]![0] as Deployment).read_only).toBe(true)
  })
})
```

- [x] **Step 2: Run component test and confirm failure**

Run:

```bash
cd desktop && pnpm test src/components/Settings/__tests__/DeploymentForm.test.ts
```

Expected: failure because `[data-test="dep-read-only"]` does not exist.

- [x] **Step 3: Implement toggle**

In `desktop/src/components/Settings/DeploymentForm.vue`, add this section after the location selector:

```vue
    <label class="dep-read-only">
      <input
        type="checkbox"
        data-test="dep-read-only"
        :checked="modelValue.read_only === true"
        @change="patch({ read_only: ($event.target as HTMLInputElement).checked })"
      />
      只读（仅查看日志）
    </label>
```

Add CSS:

```css
.dep-read-only {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--text-secondary);
  margin-bottom: 6px;
}
```

- [x] **Step 4: Run component test and confirm pass**

Run:

```bash
cd desktop && pnpm test src/components/Settings/__tests__/DeploymentForm.test.ts
```

Expected: PASS.

## Task 6: Sidebar Row Hover Actions

**Files:**
- Modify: `desktop/src/components/Sidebar/EnvGroup.vue`
- Modify: `desktop/src/components/Sidebar/__tests__/EnvGroup.test.ts`

- [x] **Step 1: Write failing sidebar tests**

In `desktop/src/components/Sidebar/__tests__/EnvGroup.test.ts`, import `vi`:

```ts
import { beforeEach, describe, expect, it, vi } from 'vitest'
```

Change `makeService` to accept deployment overrides:

```ts
const makeService = (id: string, name: string, envName: string, depExtra = {}): Service => ({
  id,
  project_id: 'proj-1',
  name,
  required: false,
  order: 0,
  status: '',
  deployments: [{ id: 'dep-' + id, env_name: envName, location: 'local', status: '', ...depExtra }],
})
```

Add tests:

```ts
it('只读 deployment 不显示行内启停按钮', async () => {
  const wrapper = mount(EnvGroup, {
    props: {
      envName: 'dev',
      isDev: true,
      projectId: 'proj-1',
      services: [makeService('svc-1', 'web', 'dev', { read_only: true })],
      selectedServiceIds: new Set<string>(),
    },
  })

  await wrapper.find('[data-test="env-service-row"]').trigger('mouseenter')
  expect(wrapper.find('[data-test="row-start"]').exists()).toBe(false)
  expect(wrapper.find('[data-test="row-restart"]').exists()).toBe(false)
  expect(wrapper.find('[data-test="row-stop"]').exists()).toBe(false)
})

it('停止状态显示启动按钮，点击后只启动 deployment 不打开日志', async () => {
  const wrapper = mount(EnvGroup, {
    props: {
      envName: 'dev',
      isDev: true,
      projectId: 'proj-1',
      services: [makeService('svc-1', 'web', 'dev', { status: '' })],
      selectedServiceIds: new Set<string>(),
    },
  })
  const vm = wrapper.vm as unknown as { agentStore: { startDeployment: ReturnType<typeof vi.fn> } }
  vi.spyOn(vm.agentStore, 'startDeployment').mockResolvedValue(undefined)

  await wrapper.find('[data-test="env-service-row"]').trigger('mouseenter')
  await wrapper.find('[data-test="row-start"]').trigger('click')

  expect(vm.agentStore.startDeployment).toHaveBeenCalledWith('dep-svc-1')
  expect(wrapper.emitted('open-deployment')).toBeFalsy()
})

it('运行状态显示重启和停止按钮', async () => {
  const wrapper = mount(EnvGroup, {
    props: {
      envName: 'dev',
      isDev: true,
      projectId: 'proj-1',
      services: [makeService('svc-1', 'web', 'dev', { status: 'running' })],
      selectedServiceIds: new Set<string>(),
    },
  })

  await wrapper.find('[data-test="env-service-row"]').trigger('mouseenter')
  expect(wrapper.find('[data-test="row-restart"]').exists()).toBe(true)
  expect(wrapper.find('[data-test="row-stop"]').exists()).toBe(true)
  expect(wrapper.find('[data-test="row-start"]').exists()).toBe(false)
})
```

- [x] **Step 2: Run sidebar tests and confirm failure**

Run:

```bash
cd desktop && pnpm test src/components/Sidebar/__tests__/EnvGroup.test.ts
```

Expected: failure because row action buttons do not exist.

- [x] **Step 3: Implement row action helpers**

In `desktop/src/components/Sidebar/EnvGroup.vue`, add:

```ts
function isRunningStatus(status: string): boolean {
  return status === 'running' || status === 'starting'
}

function canControlDeployment(svc: Service): boolean {
  const dep = deploymentForService(svc)
  return !!dep && dep.read_only !== true
}

async function startOne(svc: Service) {
  const dep = deploymentForService(svc)
  if (!dep || dep.read_only) return
  await agentStore.startDeployment(dep.id)
}

async function stopOne(svc: Service) {
  const dep = deploymentForService(svc)
  if (!dep || dep.read_only) return
  await agentStore.stopDeployment(dep.id)
}

async function restartOne(svc: Service) {
  const dep = deploymentForService(svc)
  if (!dep || dep.read_only) return
  await agentStore.restartDeployment(dep.id)
}
```

Update `stopAll` and `canStart` to skip read-only deployments:

```ts
.filter(d => d && d.read_only !== true && isRunningStatus(d.status))
```

```ts
return dep && dep.read_only !== true && !isRunningStatus(dep.status)
```

- [x] **Step 4: Implement row action template**

Inside each `.env-service-row`, after `.service-name`, add:

```vue
        <div
          v-if="canControlDeployment(svc)"
          class="row-actions"
          data-test="row-actions"
          @click.stop
          @pointerdown.stop
        >
          <button
            v-if="!isRunningStatus(deploymentForService(svc)?.status ?? '')"
            type="button"
            class="row-action start"
            data-test="row-start"
            title="启动"
            @click="startOne(svc)"
          >▶</button>
          <button
            v-if="isRunningStatus(deploymentForService(svc)?.status ?? '')"
            type="button"
            class="row-action restart"
            data-test="row-restart"
            title="重启"
            @click="restartOne(svc)"
          >↻</button>
          <button
            v-if="isRunningStatus(deploymentForService(svc)?.status ?? '')"
            type="button"
            class="row-action stop"
            data-test="row-stop"
            title="停止"
            @click="stopOne(svc)"
          >⏹</button>
        </div>
```

- [x] **Step 5: Add sliding CSS**

Add to `desktop/src/components/Sidebar/EnvGroup.vue` CSS:

```css
.row-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  opacity: 0;
  transform: translateX(8px);
  transition: opacity 0.14s ease, transform 0.14s ease;
  pointer-events: none;
  flex-shrink: 0;
}

.env-service-row:hover .row-actions {
  opacity: 1;
  transform: translateX(0);
  pointer-events: auto;
}

.row-action {
  width: 20px;
  height: 20px;
  border: none;
  border-radius: 4px;
  background: rgba(255, 255, 255, 0.06);
  color: var(--text-secondary, #8b949e);
  font-size: 11px;
  cursor: pointer;
  line-height: 20px;
  padding: 0;
}

.row-action:hover {
  background: rgba(255, 255, 255, 0.12);
}

.row-action.start {
  color: #3fb950;
}

.row-action.restart {
  color: #d29922;
}

.row-action.stop {
  color: #f85149;
}
```

- [x] **Step 6: Run sidebar tests and confirm pass**

Run:

```bash
cd desktop && pnpm test src/components/Sidebar/__tests__/EnvGroup.test.ts
```

Expected: PASS.

## Task 7: Full Verification

**Files:**
- Verify only; no planned edits.

- [x] **Step 1: Format Go files**

Run:

```bash
gofmt -w agent/model/model.go agent/model/model_test.go agent/config/loader.go agent/config/loader_test.go agent/api/handler_deployments.go agent/api/api_test.go
```

Expected: command exits 0.

- [x] **Step 2: Run focused Go tests**

Run:

```bash
go test ./model ./config ./api -count=1
```

Expected: PASS.

- [x] **Step 3: Run focused frontend tests**

Run:

```bash
cd desktop && pnpm test src/lib/__tests__/configDraft.test.ts src/components/Settings/__tests__/DeploymentForm.test.ts src/components/Sidebar/__tests__/EnvGroup.test.ts
```

Expected: PASS.

- [x] **Step 4: Run frontend build**

Run:

```bash
cd desktop && pnpm build
```

Expected: PASS. Do not start the dev server.

- [x] **Step 5: Inspect diff**

Run:

```bash
git diff -- agent/model/model.go agent/model/model_test.go agent/config/loader.go agent/config/loader_test.go agent/api/handler_deployments.go agent/api/api_test.go desktop/src/api/agent.ts desktop/src/lib/configDraft.ts desktop/src/lib/__tests__/configDraft.test.ts desktop/src/components/Settings/DeploymentForm.vue desktop/src/components/Settings/__tests__/DeploymentForm.test.ts desktop/src/components/Sidebar/EnvGroup.vue desktop/src/components/Sidebar/__tests__/EnvGroup.test.ts
```

Expected: diff only includes explicit read-only support, API guards, config form toggle, sidebar row actions, and tests. Existing unrelated dirty changes remain untouched.

## Self-Review

- Spec coverage: data model, YAML/JSON propagation, backend start/stop/restart guard, config UI toggle, sidebar hover actions, and tests are all mapped to tasks.
- Placeholder scan: no TBD/TODO/fill-in steps; all code edits include exact snippets.
- Type consistency: field name is consistently `read_only` in JSON/YAML/TypeScript and `ReadOnly` in Go.
- Scope check: plan stays within deployment field, existing deployment API, configuration editor, and sidebar row controls; it does not introduce sudo or new control channels.
