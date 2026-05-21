# Desktop Log Rules Settings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore recent logs in desktop log panels, replace the old history/run toolbar with project rule management, and add a Tauri/Vue settings page for general and project settings.

**Architecture:** Keep the existing Tauri/Vue + Pinia + Go agent boundaries. Agent owns persistent project rules and global log retention settings; the Vue app owns panel-local filters, local UI preferences, route-level settings UI, and Tauri autostart integration. Each feature is implemented through narrow store/component changes with tests before production code.

**Tech Stack:** Vue 3, Pinia, Vue Router, Vitest, Tauri v2, `@tauri-apps/plugin-autostart`, Go 1.22 HTTP handlers, SQLite store.

---

## Scope Check

The approved spec touches three related areas: log panel startup context, rule management, and settings. They share the same desktop shell and agent API surface, so this plan keeps them together but splits execution into independent vertical tasks with their own tests and commits.

## File Structure

### Agent

- Create `agent/config/settings.go`: load, validate, and save agent-level settings in `settings.json`.
- Create `agent/api/handler_settings.go`: HTTP handlers for `GET /api/settings` and `PUT /api/settings`.
- Modify `agent/api/server.go`: add routes and prune logs during app initialization.
- Modify `agent/api/api_test.go`: add settings endpoint and retention pruning tests.

### Desktop API And Stores

- Modify `desktop/package.json`: add `@tauri-apps/plugin-autostart`.
- Modify `desktop/src-tauri/Cargo.toml`: add `tauri-plugin-autostart`.
- Modify `desktop/src-tauri/src/main.rs`: register the autostart plugin and route tray settings menu to `#/settings`.
- Modify `desktop/src/api/agent.ts`: add `AgentSettings`, `getSettings`, and `putSettings`.
- Create `desktop/src/stores/settings.ts`: agent settings, autostart, and local hidden-service preference store.
- Create `desktop/src/stores/__tests__/settings.test.ts`: settings store tests.

### Log Panel Startup History

- Modify `desktop/src/stores/log.ts`: bootstrap recent history once per service, dedupe REST history and WebSocket recent logs, expose history boundary metadata.
- Modify `desktop/src/lib/logDisplay.ts`: add a synthetic history separator display item.
- Modify `desktop/src/lib/__tests__/logDisplay.test.ts`: cover separator placement and stats exclusion.
- Create `desktop/src/components/Panel/LogHistorySeparatorRow.vue`: render the divider row.
- Modify `desktop/src/components/Panel/LogPanel.vue`: remove run-history mode and render the separator.
- Modify `desktop/src/components/Panel/PanelToolbar.vue`: remove old history button props/events.
- Create `desktop/src/stores/__tests__/log.test.ts`: log store bootstrap and dedupe tests.

### Rule Management

- Modify `desktop/src/stores/filter.ts`: add rule create/update/delete/save-from-panel actions and make `toggleRule` await persistence.
- Modify `desktop/src/stores/__tests__/filter.test.ts`: cover rule actions and save-from-panel.
- Create `desktop/src/components/Panel/RuleManagerModal.vue`: project rule management UI.
- Create `desktop/src/components/Panel/__tests__/RuleManagerModal.test.ts`: modal behavior tests.
- Modify `desktop/src/components/Panel/PanelToolbar.vue`: larger gear button opens modal; current chip state passed to the modal.

### Settings Page And Popover Preferences

- Create `desktop/src/pages/SettingsPage.vue`: split settings UI with General and Projects panes.
- Create `desktop/src/pages/__tests__/SettingsPage.test.ts`: settings page tests.
- Modify `desktop/src/router/index.ts`: add `/settings`.
- Modify `desktop/src/components/Sidebar/SidebarView.vue`: add settings entry.
- Modify `desktop/src/components/Popover/PopoverProjectList.vue`: hide locally hidden services.
- Modify `desktop/src/components/Popover/PopoverServicePanel.vue`: hide locally hidden services in required/optional lists and counts.
- Create or extend popover component tests for hidden services.

---

### Task 1: Agent Settings API And Retention Pruning

**Files:**
- Create: `agent/config/settings.go`
- Create: `agent/api/handler_settings.go`
- Modify: `agent/api/server.go`
- Modify: `agent/api/api_test.go`

- [ ] **Step 1: Add failing settings endpoint tests**

Append these tests to `agent/api/api_test.go`:

```go
// TestSettingsDefaultsAndPersistence 验证 agent 设置接口返回默认值并能持久化修改。
func TestSettingsDefaultsAndPersistence(t *testing.T) {
	srv, dataDir := newTestApp(t)

	resp, err := http.Get(srv.URL + "/api/settings")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var defaults struct {
		LogRetentionDays int `json:"log_retention_days"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&defaults))
	assert.Equal(t, 7, defaults.LogRetentionDays)

	req, err := http.NewRequest(
		http.MethodPut,
		srv.URL+"/api/settings",
		strings.NewReader(`{"log_retention_days": 14}`),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer putResp.Body.Close()
	require.Equal(t, http.StatusOK, putResp.StatusCode)

	settingsPath := filepath.Join(dataDir, "settings.json")
	raw, err := os.ReadFile(settingsPath)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"log_retention_days": 14`)
}

// TestSettingsRejectsInvalidRetention 验证日志保留天数范围为 1 到 90。
func TestSettingsRejectsInvalidRetention(t *testing.T) {
	srv, _ := newTestApp(t)

	req, err := http.NewRequest(
		http.MethodPut,
		srv.URL+"/api/settings",
		strings.NewReader(`{"log_retention_days": 0}`),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestNewAppPrunesOldLogsUsingSavedSettings 验证 App 初始化时按持久化设置清理旧日志。
func TestNewAppPrunesOldLogsUsingSavedSettings(t *testing.T) {
	dataDir := t.TempDir()

	settingsPath := filepath.Join(dataDir, "settings.json")
	require.NoError(t, os.WriteFile(settingsPath, []byte(`{"log_retention_days": 3}`), 0o644))

	dbPath := filepath.Join(dataDir, "logs.db")
	s, err := store.New(dbPath)
	require.NoError(t, err)
	old := time.Now().UTC().Add(-5 * 24 * time.Hour)
	recent := time.Now().UTC()
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{ServiceID: "svc-1", RunID: "run-1", Timestamp: old, Level: "INFO", Message: "old", Stream: "stdout"},
		{ServiceID: "svc-1", RunID: "run-1", Timestamp: recent, Level: "INFO", Message: "recent", Stream: "stdout"},
	}))
	require.NoError(t, s.Close())

	app, err := api.NewApp(api.AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	t.Cleanup(func() { app.Close() })

	check, err := store.New(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { check.Close() })
	got, err := check.Fetch(store.FetchParams{ServiceID: "svc-1", Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "recent", got[0].Message)
}
```

Add these imports to `agent/api/api_test.go`:

```go
	"time"

	"github.com/superdev/agent/store"
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd agent && go test ./api -run 'TestSettings|TestNewAppPrunes' -count=1`

Expected: FAIL because `/api/settings` is not registered and `store` may be missing until imports are added.

- [ ] **Step 3: Create settings persistence**

Create `agent/config/settings.go`:

```go
// Package config 负责 SuperDev agent 配置文件的读写。
//
// 职责：
//   - 读写 agent 级设置文件
//   - 校验设置值范围，避免无效配置进入运行时
//
// 边界：
//   - 不执行设置对应的业务动作，例如日志清理
//   - 不读写项目级 .superdev/config.yaml
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// DefaultLogRetentionDays 是日志保留天数的默认值。
	DefaultLogRetentionDays = 7
	// MinLogRetentionDays 是允许的最小日志保留天数。
	MinLogRetentionDays = 1
	// MaxLogRetentionDays 是允许的最大日志保留天数。
	MaxLogRetentionDays = 90
)

// AgentSettings 表示 agent 级全局设置。
type AgentSettings struct {
	LogRetentionDays int `json:"log_retention_days"`
}

// SettingsStore 负责读写 agent 数据目录下的 settings.json。
type SettingsStore struct {
	path string
}

// NewSettingsStore 创建一个使用 dataDir/settings.json 的设置存储。
func NewSettingsStore(dataDir string) *SettingsStore {
	return &SettingsStore{path: filepath.Join(dataDir, "settings.json")}
}

// DefaultAgentSettings 返回默认 agent 设置。
func DefaultAgentSettings() AgentSettings {
	return AgentSettings{LogRetentionDays: DefaultLogRetentionDays}
}

// ValidateAgentSettings 校验 agent 设置字段范围。
func ValidateAgentSettings(settings AgentSettings) error {
	if settings.LogRetentionDays < MinLogRetentionDays || settings.LogRetentionDays > MaxLogRetentionDays {
		return fmt.Errorf("log_retention_days must be between %d and %d", MinLogRetentionDays, MaxLogRetentionDays)
	}
	return nil
}

// Load 读取 settings.json；文件不存在时返回默认设置。
func (s *SettingsStore) Load() (AgentSettings, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultAgentSettings(), nil
	}
	if err != nil {
		return AgentSettings{}, fmt.Errorf("read settings: %w", err)
	}
	settings := DefaultAgentSettings()
	if err := json.Unmarshal(data, &settings); err != nil {
		return AgentSettings{}, fmt.Errorf("parse settings: %w", err)
	}
	if err := ValidateAgentSettings(settings); err != nil {
		return AgentSettings{}, err
	}
	return settings, nil
}

// Save 校验并写入 settings.json。
func (s *SettingsStore) Save(settings AgentSettings) error {
	if err := ValidateAgentSettings(settings); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir settings dir: %w", err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(s.path, data, 0o644)
}
```

- [ ] **Step 4: Add settings handlers and routes**

Create `agent/api/handler_settings.go`:

```go
// handler_settings.go 实现 agent 级设置 HTTP 接口。
//
// 职责：
//   - 返回当前 agent 设置
//   - 校验并持久化设置更新
//
// 边界：
//   - 不处理项目级配置
//   - 不直接渲染客户端设置页
package api

import (
	"encoding/json"
	"net/http"

	"github.com/superdev/agent/config"
)

// getSettings 处理 GET /api/settings。
func (a *App) getSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := a.settings.Load()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to load settings: "+err.Error())
		return
	}
	jsonOK(w, settings)
}

// putSettings 处理 PUT /api/settings。
func (a *App) putSettings(w http.ResponseWriter, r *http.Request) {
	var settings config.AgentSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := a.settings.Save(settings); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, settings)
}
```

Modify `agent/api/server.go`:

```go
type App struct {
	cfg      AppConfig
	mu       sync.RWMutex
	projects []model.Project
	managers map[string]*process.Manager
	buf      *logbuf.Buffer
	store    *store.Store
	registry *config.Registry
	settings *config.SettingsStore
}
```

In `NewApp`, create the settings store and prune logs before constructing the App:

```go
	settingsStore := config.NewSettingsStore(cfg.DataDir)
	settings, err := settingsStore.Load()
	if err != nil {
		s.Close()
		return nil, err
	}
	if err := s.DeleteOlderThan(settings.LogRetentionDays); err != nil {
		s.Close()
		return nil, err
	}

	buf := logbuf.New(s, 2000)
	registryPath := filepath.Join(cfg.DataDir, "projects.json")
	registry := config.NewRegistry(registryPath)

	return &App{
		cfg:      cfg,
		projects: []model.Project{},
		managers: map[string]*process.Manager{},
		buf:      buf,
		store:    s,
		registry: registry,
		settings: settingsStore,
	}, nil
```

In `Handler`, register settings routes near project management:

```go
	mux.HandleFunc("GET /api/settings", a.getSettings)
	mux.HandleFunc("PUT /api/settings", a.putSettings)
```

- [ ] **Step 5: Run tests**

Run: `cd agent && go test ./...`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add agent/config/settings.go agent/api/handler_settings.go agent/api/server.go agent/api/api_test.go
git commit -m "feat(agent): add settings api"
```

---

### Task 2: Desktop Settings Store, API Client, And Autostart Plugin

**Files:**
- Modify: `desktop/package.json`
- Modify: `desktop/src-tauri/Cargo.toml`
- Modify: `desktop/src-tauri/src/main.rs`
- Modify: `desktop/src/api/agent.ts`
- Create: `desktop/src/stores/settings.ts`
- Create: `desktop/src/stores/__tests__/settings.test.ts`

- [ ] **Step 1: Add failing settings store tests**

Create `desktop/src/stores/__tests__/settings.test.ts`:

```ts
/**
 * settingsStore 测试桌面端通用设置和本地 UI 偏好。
 *
 * 职责：
 *   - 验证日志保留天数通过 agent API 读写
 *   - 验证服务显示/隐藏偏好持久化在 localStorage
 *
 * 边界：
 *   - 不调用真实 Tauri autostart 插件
 *   - 不渲染设置页组件
 */
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api as agentApi } from '@/api/agent'
import { useSettingsStore } from '../settings'

vi.mock('@tauri-apps/plugin-autostart', () => ({
  enable: vi.fn().mockResolvedValue(undefined),
  disable: vi.fn().mockResolvedValue(undefined),
  isEnabled: vi.fn().mockResolvedValue(false),
}))

describe('settingsStore', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
    setActivePinia(createPinia())
  })

  it('loadAgentSettings 从 agent 加载日志保留天数', async () => {
    vi.spyOn(agentApi, 'getSettings').mockResolvedValue({ log_retention_days: 14 })
    const store = useSettingsStore()

    await store.loadAgentSettings()

    expect(store.agentSettings.log_retention_days).toBe(14)
  })

  it('saveLogRetentionDays 持久化到 agent 并更新本地状态', async () => {
    vi.spyOn(agentApi, 'putSettings').mockResolvedValue({ log_retention_days: 21 })
    const store = useSettingsStore()

    await store.saveLogRetentionDays(21)

    expect(agentApi.putSettings).toHaveBeenCalledWith({ log_retention_days: 21 })
    expect(store.agentSettings.log_retention_days).toBe(21)
  })

  it('toggleServiceHidden 将隐藏服务偏好写入 localStorage', () => {
    const store = useSettingsStore()

    store.toggleServiceHidden('svc-api')

    expect(store.isServiceHidden('svc-api')).toBe(true)
    expect(JSON.parse(localStorage.getItem('superdev.hidden_service_ids.v1') ?? '[]')).toEqual(['svc-api'])
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd desktop && pnpm exec vitest run src/stores/__tests__/settings.test.ts`

Expected: FAIL because `desktop/src/stores/settings.ts` does not exist.

- [ ] **Step 3: Add API client types and methods**

Modify `desktop/src/api/agent.ts`:

```ts
export interface AgentSettings {
  log_retention_days: number
}
```

Add methods inside `api`:

```ts
  // 设置
  getSettings: () => request<AgentSettings>('/api/settings'),
  putSettings: (settings: AgentSettings) =>
    request<AgentSettings>('/api/settings', { method: 'PUT', body: JSON.stringify(settings) }),
```

- [ ] **Step 4: Add settings store**

Create `desktop/src/stores/settings.ts`:

```ts
// settingsStore 管理桌面端设置页状态和本地 UI 偏好。
//
// 职责：
//   - 读写 agent 级通用设置
//   - 读写 Tauri 开机自启状态
//   - 持久化服务显示/隐藏偏好
//
// 边界：
//   - 不管理项目列表和服务生命周期
//   - 不直接渲染设置页
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, type AgentSettings } from '@/api/agent'

const HIDDEN_SERVICE_IDS_KEY = 'superdev.hidden_service_ids.v1'

function loadHiddenServiceIds(): string[] {
  try {
    const raw = localStorage.getItem(HIDDEN_SERVICE_IDS_KEY)
    const parsed = raw ? JSON.parse(raw) : []
    return Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === 'string') : []
  } catch {
    return []
  }
}

function saveHiddenServiceIds(ids: string[]) {
  localStorage.setItem(HIDDEN_SERVICE_IDS_KEY, JSON.stringify(ids))
}

export const useSettingsStore = defineStore('settings', () => {
  const agentSettings = ref<AgentSettings>({ log_retention_days: 7 })
  const hiddenServiceIds = ref<string[]>(loadHiddenServiceIds())
  const autostartEnabled = ref(false)
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function loadAgentSettings() {
    loading.value = true
    error.value = null
    try {
      agentSettings.value = await api.getSettings()
    } catch (err) {
      error.value = err instanceof Error ? err.message : String(err)
    } finally {
      loading.value = false
    }
  }

  async function saveLogRetentionDays(days: number) {
    const saved = await api.putSettings({ log_retention_days: days })
    agentSettings.value = saved
  }

  async function loadAutostart() {
    const { isEnabled } = await import('@tauri-apps/plugin-autostart')
    autostartEnabled.value = await isEnabled()
  }

  async function setAutostart(enabled: boolean) {
    const { enable, disable } = await import('@tauri-apps/plugin-autostart')
    if (enabled) await enable()
    else await disable()
    autostartEnabled.value = enabled
  }

  function isServiceHidden(serviceId: string): boolean {
    return hiddenServiceIds.value.includes(serviceId)
  }

  function toggleServiceHidden(serviceId: string) {
    const next = hiddenServiceIds.value.includes(serviceId)
      ? hiddenServiceIds.value.filter(id => id !== serviceId)
      : [...hiddenServiceIds.value, serviceId]
    hiddenServiceIds.value = next
    saveHiddenServiceIds(next)
  }

  return {
    agentSettings,
    hiddenServiceIds,
    autostartEnabled,
    loading,
    error,
    loadAgentSettings,
    saveLogRetentionDays,
    loadAutostart,
    setAutostart,
    isServiceHidden,
    toggleServiceHidden,
  }
})
```

- [ ] **Step 5: Register Tauri autostart plugin**

Install dependencies:

```bash
cd desktop
pnpm add @tauri-apps/plugin-autostart
cd src-tauri
cargo add tauri-plugin-autostart
```

Modify `desktop/src-tauri/src/main.rs` imports:

```rust
use tauri_plugin_autostart::MacosLauncher;
```

Register the plugin in the builder, after existing plugins:

```rust
        .plugin(tauri_plugin_autostart::init(
            MacosLauncher::LaunchAgent,
            None,
        ))
```

This follows the official Tauri v2 autostart plugin shape: <https://v2.tauri.app/plugin/autostart/>.

- [ ] **Step 6: Route tray Settings menu to the settings page**

Add this helper to `desktop/src-tauri/src/main.rs`:

```rust
/// show_settings_window 显示主窗口并切换到设置页。
fn show_settings_window(app: &tauri::AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.show();
        let _ = w.eval("window.location.hash = '#/settings'");
        let _ = w.set_focus();
    }
}
```

Replace the existing `"settings"` menu branch:

```rust
                    "settings" => {
                        show_settings_window(app);
                    }
```

- [ ] **Step 7: Run tests**

Run: `cd desktop && pnpm exec vitest run src/stores/__tests__/settings.test.ts`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add desktop/package.json desktop/pnpm-lock.yaml desktop/src-tauri/Cargo.toml desktop/src-tauri/Cargo.lock desktop/src-tauri/src/main.rs desktop/src/api/agent.ts desktop/src/stores/settings.ts desktop/src/stores/__tests__/settings.test.ts
git commit -m "feat(desktop): add settings store"
```

---

### Task 3: Log Panel Recent History Bootstrap And Separator

**Files:**
- Create: `desktop/src/stores/__tests__/log.test.ts`
- Modify: `desktop/src/stores/log.ts`
- Modify: `desktop/src/lib/logDisplay.ts`
- Modify: `desktop/src/lib/__tests__/logDisplay.test.ts`
- Create: `desktop/src/components/Panel/LogHistorySeparatorRow.vue`
- Modify: `desktop/src/components/Panel/LogPanel.vue`
- Modify: `desktop/src/components/Panel/PanelToolbar.vue`

- [ ] **Step 1: Add failing log store tests**

Create `desktop/src/stores/__tests__/log.test.ts`:

```ts
/**
 * logStore 测试服务日志订阅时的历史恢复。
 *
 * 职责：
 *   - 验证订阅服务时先拉取最近历史日志
 *   - 验证 REST 历史和 WebSocket recent 重复时不会重复显示
 *
 * 边界：
 *   - 不建立真实 WebSocket
 *   - 不挂载日志面板组件
 */
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api as agentApi, type LogEntry } from '@/api/agent'
import { useLogStore } from '../log'

class MockWebSocket {
  static OPEN = 1
  static CLOSED = 3
  static instances: MockWebSocket[] = []
  onmessage: ((event: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  readyState = WebSocket.OPEN

  constructor(public url: string) {
    MockWebSocket.instances.push(this)
  }

  close() {
    this.readyState = WebSocket.CLOSED
    this.onclose?.()
  }

  emit(entry: LogEntry) {
    this.onmessage?.({ data: JSON.stringify(entry) })
  }
}

function log(id: number, message: string): LogEntry {
  return {
    id,
    service_id: 'svc-api',
    run_id: 'run-1',
    timestamp: `2026-05-21T10:00:0${id}.000Z`,
    level: 'INFO',
    message,
    stream: 'stdout',
  }
}

describe('logStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
    MockWebSocket.instances = []
    vi.stubGlobal('WebSocket', MockWebSocket)
  })

  it('subscribe 先加载最近历史日志并记录历史边界', async () => {
    vi.spyOn(agentApi, 'fetchLogs').mockResolvedValue([log(1, 'history 1'), log(2, 'history 2')])
    const store = useLogStore()

    await store.subscribe('svc-api')

    expect(agentApi.fetchLogs).toHaveBeenCalledWith({ service: 'svc-api', limit: 200 })
    expect(store.getLogs('svc-api').map(l => l.message)).toEqual(['history 1', 'history 2'])
    expect(store.getHistoryBoundary('svc-api')).toEqual({ timestamp: '2026-05-21T10:00:02.000Z', id: 2 })
  })

  it('WebSocket recent 与历史接口重复时按日志签名去重', async () => {
    vi.spyOn(agentApi, 'fetchLogs').mockResolvedValue([log(1, 'history 1')])
    const store = useLogStore()

    await store.subscribe('svc-api')
    MockWebSocket.instances[0].emit(log(1, 'history 1'))
    MockWebSocket.instances[0].emit(log(2, 'live 2'))

    expect(store.getLogs('svc-api').map(l => l.message)).toEqual(['history 1', 'live 2'])
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd desktop && pnpm exec vitest run src/stores/__tests__/log.test.ts`

Expected: FAIL because `subscribe` is synchronous and `getHistoryBoundary` does not exist.

- [ ] **Step 3: Implement log bootstrap and dedupe**

Modify `desktop/src/stores/log.ts`:

```ts
interface LogBoundary {
  timestamp: string
  id: number
}

interface ServiceLog {
  logs: DisplayLogEntry[]
  ws: WebSocket | null
  refCount: number
  bootstrapPromise: Promise<void> | null
  historyBoundary: LogBoundary | null
  seenSignatures: Set<string>
}

function logSignature(log: LogEntry): string {
  return [
    log.service_id,
    log.run_id,
    log.timestamp,
    log.level,
    log.stream,
    log.message,
  ].join('\u001f')
}
```

Update `getOrCreate`:

```ts
serviceLogs.value[serviceId] = {
  logs: [],
  ws: null,
  refCount: 0,
  bootstrapPromise: null,
  historyBoundary: null,
  seenSignatures: new Set(),
}
```

Add helpers:

```ts
function appendEntry(entry: ServiceLog, log: LogEntry): boolean {
  const sig = logSignature(log)
  if (entry.seenSignatures.has(sig)) return false
  entry.seenSignatures.add(sig)
  ingest(toDisplayEntry(log), entry.logs)
  if (entry.logs.length > MAX_LOGS) {
    entry.logs.splice(0, entry.logs.length - MAX_LOGS)
  }
  return true
}

async function bootstrapRecent(serviceId: string, entry: ServiceLog) {
  if (entry.bootstrapPromise) return entry.bootstrapPromise
  entry.bootstrapPromise = (async () => {
    const { api } = await import('@/api/agent')
    let logs: LogEntry[] = []
    try {
      logs = await api.fetchLogs({ service: serviceId, limit: 200 })
    } catch (err) {
      console.warn('[SuperDev] load recent logs failed:', err)
      return
    }
    for (const log of logs) appendEntry(entry, log)
    const last = logs[logs.length - 1]
    entry.historyBoundary = last ? { timestamp: last.timestamp, id: last.id } : null
    if (logs.length > 0) bumpRevision()
  })()
  return entry.bootstrapPromise
}
```

Change `subscribe` to `async function subscribe(serviceId: string)` and call `await bootstrapRecent(serviceId, entry)` before opening the WebSocket. In `ws.onmessage`, replace direct `ingest(...)` with:

```ts
if (appendEntry(entry, log)) bumpRevision()
```

Add:

```ts
function getHistoryBoundary(serviceId: string): LogBoundary | null {
  return serviceLogs.value[serviceId]?.historyBoundary ?? null
}
```

Return `getHistoryBoundary`.

- [ ] **Step 4: Add failing display separator tests**

Append to `desktop/src/lib/__tests__/logDisplay.test.ts`:

```ts
  it('在历史边界后插入历史消息分隔线', () => {
    const logs = [
      makeLog(1, '2026-05-21T10:00:01.000Z'),
      makeLog(2, '2026-05-21T10:00:02.000Z'),
      makeLog(3, '2026-05-21T10:00:03.000Z'),
    ]

    const items = makeDisplayItems(logs, null, markers, {
      timestamp: '2026-05-21T10:00:02.000Z',
      id: 2,
    })

    expect(items.map(item => item.kind)).toEqual(['entry', 'entry', 'historySeparator', 'entry'])
  })

  it('历史分隔线不参与统计', () => {
    const logs = [
      makeLog(1, '2026-05-21T10:00:01.000Z'),
      makeLog(2, '2026-05-21T10:00:02.000Z'),
    ]

    const items = makeDisplayItems(logs, null, markers, {
      timestamp: '2026-05-21T10:00:01.000Z',
      id: 1,
    })

    expect(computeDisplayStats(items).total).toBe(2)
  })
```

Add `computeDisplayStats` to the existing import:

```ts
import { makeDisplayItems, computeDisplayStats } from '../logDisplay'
```

- [ ] **Step 5: Implement separator display item**

Modify `desktop/src/lib/logDisplay.ts`:

```ts
export type LogDisplayItem =
  | { kind: 'entry'; id: string; log: DisplayLogEntry }
  | { kind: 'markerStart'; id: string; date: Date }
  | { kind: 'markerEnd'; id: string; date: Date }
  | { kind: 'historySeparator'; id: string }

export interface HistoryBoundary {
  timestamp: string
  id: number
}

function isAtOrBeforeBoundary(log: DisplayLogEntry, boundary: HistoryBoundary): boolean {
  const diff = new Date(log.timestamp).getTime() - new Date(boundary.timestamp).getTime()
  return diff < 0 || (diff === 0 && log.id <= boundary.id)
}

function withHistorySeparator(items: LogDisplayItem[], boundary: HistoryBoundary | null): LogDisplayItem[] {
  if (!boundary) return items
  let insertAfter = -1
  for (let i = 0; i < items.length; i++) {
    const item = items[i]
    if (item.kind === 'entry' && isAtOrBeforeBoundary(item.log, boundary)) {
      insertAfter = i
    }
  }
  if (insertAfter < 0) return items
  return [
    ...items.slice(0, insertAfter + 1),
    { kind: 'historySeparator', id: `history-separator-${boundary.timestamp}-${boundary.id}` },
    ...items.slice(insertAfter + 1),
  ]
}
```

Change the signature:

```ts
export function makeDisplayItems(
  logs: DisplayLogEntry[],
  bm: BookmarkDisplayInput | null,
  markerIds: MarkerIds,
  historyBoundary: HistoryBoundary | null = null,
): LogDisplayItem[] {
```

Wrap each return:

```ts
return withHistorySeparator(items, historyBoundary)
```

Keep `computeDisplayStats` unchanged except it will naturally skip non-entry items because it already checks `item.kind !== 'entry'`.

- [ ] **Step 6: Render separator and remove run-history UI**

Create `desktop/src/components/Panel/LogHistorySeparatorRow.vue`:

```vue
<!--
历史日志分隔行

职责：
  - 标出日志面板中最近历史日志与实时输出的边界

边界：
  - 不代表真实 LogEntry
  - 不参与复制、导出或过滤
-->
<template>
  <div class="history-separator-row">
    <span class="line" />
    <span class="label">历史消息 · 之后为实时输出</span>
    <span class="line" />
  </div>
</template>

<style scoped>
.history-separator-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 12px;
  color: var(--text-tertiary);
  font-size: 10px;
}
.line {
  height: 1px;
  flex: 1;
  background: var(--border-secondary);
}
.label {
  white-space: nowrap;
}
</style>
```

Modify `desktop/src/components/Panel/LogPanel.vue`:

- Import `LogHistorySeparatorRow`.
- Remove `viewingRunId`, `historyRunIds`, `selectRun`, `logStore.getHistoryLogs`, and the yellow history banner.
- Use only `logStore.getLogs(props.serviceId)` for service panels.
- Pass the boundary to `makeDisplayItems`:

```ts
const historyBoundary = computed(() =>
  props.serviceId ? logStore.getHistoryBoundary(props.serviceId) : null,
)

const items = makeDisplayItems(logs, displayBm, {
  start: markerStartId.value,
  end: markerEndId.value,
}, historyBoundary.value)
```

Render the new item:

```vue
<LogHistorySeparatorRow
  v-else-if="item.kind === 'historySeparator'"
/>
```

Update the status text:

```vue
实时 · 显示 {{ stats.total }} 条
```

Modify `desktop/src/components/Panel/PanelToolbar.vue`:

- Remove props `historyRunIds` and `viewingRunId`.
- Remove emit `selectRun`.
- Delete the history button:

```vue
<button class="icon-btn" title="历史记录" @click="emit('selectRun', null)">
  🕐 历史
</button>
```

- [ ] **Step 7: Run tests**

Run:

```bash
cd desktop
pnpm exec vitest run src/stores/__tests__/log.test.ts src/lib/__tests__/logDisplay.test.ts
pnpm build
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add desktop/src/stores/log.ts desktop/src/stores/__tests__/log.test.ts desktop/src/lib/logDisplay.ts desktop/src/lib/__tests__/logDisplay.test.ts desktop/src/components/Panel/LogHistorySeparatorRow.vue desktop/src/components/Panel/LogPanel.vue desktop/src/components/Panel/PanelToolbar.vue
git commit -m "feat(desktop): restore recent panel logs"
```

---

### Task 4: Filter Store Rule Actions

**Files:**
- Modify: `desktop/src/stores/filter.ts`
- Modify: `desktop/src/stores/__tests__/filter.test.ts`

- [ ] **Step 1: Add failing filter rule action tests**

Append to `desktop/src/stores/__tests__/filter.test.ts`:

```ts
import { vi } from 'vitest'
import { api as agentApi, type LogRule } from '@/api/agent'
```

Append these test cases inside `describe('filterStore', () => { ... })`:

```ts
  it('createRule 新增项目规则并持久化', async () => {
    const store = useFilterStore()
    store.projectRules['proj-1'] = []
    vi.spyOn(agentApi, 'putProjectRules').mockImplementation(async (_projectId, rules) => rules)

    await store.createRule('proj-1', {
      name: 'Errors',
      type: 'include',
      keywords: ['error'],
      logic: 'or',
      enabled: true,
    })

    expect(store.projectRules['proj-1']).toHaveLength(1)
    expect(store.projectRules['proj-1'][0].name).toBe('Errors')
    expect(agentApi.putProjectRules).toHaveBeenCalledWith('proj-1', store.projectRules['proj-1'])
  })

  it('savePanelChipsAsRule 将当前面板 chip 保存为项目规则', async () => {
    const store = useFilterStore()
    store.projectRules['proj-1'] = []
    store.addChip('panel-1', 'error', 'include')
    store.addChip('panel-1', 'timeout', 'include')
    vi.spyOn(agentApi, 'putProjectRules').mockImplementation(async (_projectId, rules) => rules)

    await store.savePanelChipsAsRule('proj-1', 'panel-1', {
      name: 'Errors and timeouts',
      type: 'include',
      logic: 'or',
      enabled: true,
    })

    const saved = store.projectRules['proj-1'][0]
    expect(saved.keywords).toEqual(['error', 'timeout'])
    expect(saved.name).toBe('Errors and timeouts')
  })

  it('deleteRule 删除项目规则并持久化', async () => {
    const store = useFilterStore()
    const rule: LogRule = {
      id: 'rule-1',
      name: 'Noise',
      type: 'exclude',
      keywords: ['health'],
      logic: 'or',
      enabled: true,
    }
    store.projectRules['proj-1'] = [rule]
    vi.spyOn(agentApi, 'putProjectRules').mockImplementation(async (_projectId, rules) => rules)

    await store.deleteRule('proj-1', 'rule-1')

    expect(store.projectRules['proj-1']).toEqual([])
  })
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd desktop && pnpm exec vitest run src/stores/__tests__/filter.test.ts`

Expected: FAIL because rule action methods do not exist.

- [ ] **Step 3: Add rule action methods**

Modify `desktop/src/stores/filter.ts`:

```ts
export interface RuleDraft {
  name: string
  type: ChipType
  keywords: string[]
  logic: ChipLogic
  enabled: boolean
}

export interface PanelRuleDraft {
  name: string
  type: ChipType
  logic: ChipLogic
  enabled: boolean
}
```

Add helper:

```ts
function cleanKeywords(keywords: string[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const keyword of keywords) {
    const trimmed = keyword.trim()
    const key = trimmed.toLowerCase()
    if (!trimmed || seen.has(key)) continue
    seen.add(key)
    out.push(trimmed)
  }
  return out
}
```

Add actions:

```ts
async function createRule(projectId: string, draft: RuleDraft) {
  const current = projectRules.value[projectId] ?? []
  const rule: LogRule = {
    id: uuidv4(),
    name: draft.name.trim() || draft.keywords[0] || '未命名规则',
    type: draft.type,
    keywords: cleanKeywords(draft.keywords),
    logic: draft.logic,
    enabled: draft.enabled,
  }
  if (rule.keywords.length === 0) return
  await saveProjectRules(projectId, [...current, rule])
}

async function updateRule(projectId: string, ruleId: string, draft: RuleDraft) {
  const current = projectRules.value[projectId] ?? []
  const next = current.map(rule =>
    rule.id === ruleId
      ? {
          ...rule,
          name: draft.name.trim() || draft.keywords[0] || rule.name,
          type: draft.type,
          keywords: cleanKeywords(draft.keywords),
          logic: draft.logic,
          enabled: draft.enabled,
        }
      : rule,
  ).filter(rule => rule.keywords.length > 0)
  await saveProjectRules(projectId, next)
}

async function deleteRule(projectId: string, ruleId: string) {
  const current = projectRules.value[projectId] ?? []
  await saveProjectRules(projectId, current.filter(rule => rule.id !== ruleId))
}

async function savePanelChipsAsRule(projectId: string, panelId: string, draft: PanelRuleDraft) {
  const panel = getPanel(panelId)
  const keywords = panel.chips
    .filter(chip => chip.type === draft.type)
    .map(chip => chip.keyword)
  await createRule(projectId, {
    ...draft,
    keywords,
  })
}
```

Change `toggleRule` to await persistence:

```ts
async function toggleRule(projectId: string, ruleId: string) {
  const rules = projectRules.value[projectId]
  if (!rules) return
  const next = rules.map(rule =>
    rule.id === ruleId ? { ...rule, enabled: !rule.enabled } : rule,
  )
  await saveProjectRules(projectId, next)
}
```

Return the new methods.

- [ ] **Step 4: Run tests**

Run: `cd desktop && pnpm exec vitest run src/stores/__tests__/filter.test.ts`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add desktop/src/stores/filter.ts desktop/src/stores/__tests__/filter.test.ts
git commit -m "feat(desktop): add rule store actions"
```

---

### Task 5: Rule Manager Modal And Toolbar Integration

**Files:**
- Create: `desktop/src/components/Panel/RuleManagerModal.vue`
- Create: `desktop/src/components/Panel/__tests__/RuleManagerModal.test.ts`
- Modify: `desktop/src/components/Panel/PanelToolbar.vue`

- [ ] **Step 1: Add failing modal tests**

Create `desktop/src/components/Panel/__tests__/RuleManagerModal.test.ts`:

```ts
/**
 * RuleManagerModal 测试项目级过滤规则管理交互。
 *
 * 职责：
 *   - 验证已有规则渲染
 *   - 验证新增规则提交到 filterStore
 *   - 验证当前面板 chip 可保存为项目规则
 *
 * 边界：
 *   - 不测试后端持久化细节，store 测试覆盖 API 调用
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RuleManagerModal from '../RuleManagerModal.vue'
import { useFilterStore } from '@/stores/filter'

describe('RuleManagerModal', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('渲染当前项目规则', () => {
    const store = useFilterStore()
    store.projectRules['proj-1'] = [{
      id: 'rule-1',
      name: 'Errors',
      type: 'include',
      keywords: ['error'],
      logic: 'or',
      enabled: true,
    }]

    const wrapper = mount(RuleManagerModal, {
      props: { projectId: 'proj-1', panelId: 'panel-1' },
    })

    expect(wrapper.text()).toContain('Errors')
    expect(wrapper.text()).toContain('error')
  })

  it('提交新增规则', async () => {
    const store = useFilterStore()
    store.projectRules['proj-1'] = []
    vi.spyOn(store, 'createRule').mockResolvedValue(undefined)
    const wrapper = mount(RuleManagerModal, {
      props: { projectId: 'proj-1', panelId: 'panel-1' },
    })

    await wrapper.find('[data-test="new-rule"]').trigger('click')
    await wrapper.find('[data-test="rule-name"]').setValue('Errors')
    await wrapper.find('[data-test="rule-keywords"]').setValue('error, timeout')
    await wrapper.find('[data-test="save-rule"]').trigger('click')

    expect(store.createRule).toHaveBeenCalledWith('proj-1', {
      name: 'Errors',
      type: 'include',
      keywords: ['error', 'timeout'],
      logic: 'or',
      enabled: true,
    })
  })

  it('从当前面板 chip 保存规则', async () => {
    const store = useFilterStore()
    store.projectRules['proj-1'] = []
    store.addChip('panel-1', 'error', 'include')
    vi.spyOn(store, 'savePanelChipsAsRule').mockResolvedValue(undefined)
    const wrapper = mount(RuleManagerModal, {
      props: { projectId: 'proj-1', panelId: 'panel-1' },
    })

    await wrapper.find('[data-test="save-current-filter"]').trigger('click')
    await wrapper.find('[data-test="rule-name"]').setValue('Current filter')
    await wrapper.find('[data-test="save-rule"]').trigger('click')

    expect(store.savePanelChipsAsRule).toHaveBeenCalledWith('proj-1', 'panel-1', {
      name: 'Current filter',
      type: 'include',
      logic: 'or',
      enabled: true,
    })
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd desktop && pnpm exec vitest run src/components/Panel/__tests__/RuleManagerModal.test.ts`

Expected: FAIL because `RuleManagerModal.vue` does not exist.

- [ ] **Step 3: Create rule manager modal**

Create `desktop/src/components/Panel/RuleManagerModal.vue`:

```vue
<!--
项目过滤规则管理弹层

职责：
  - 管理当前项目的 LogRule 列表
  - 支持从当前面板临时 chip 保存为项目规则

边界：
  - 不执行日志过滤计算
  - 不管理跨服务搜索页的隐藏服务状态
-->
<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useFilterStore, type ChipLogic, type ChipType } from '@/stores/filter'
import type { LogRule } from '@/api/agent'

const props = defineProps<{
  projectId: string
  panelId: string
}>()

const emit = defineEmits<{
  close: []
}>()

const filterStore = useFilterStore()
const editingRuleId = ref<string | null>(null)
const savingFromPanel = ref(false)
const form = reactive({
  name: '',
  type: 'include' as ChipType,
  keywords: '',
  logic: 'or' as ChipLogic,
  enabled: true,
})

const rules = computed(() => filterStore.projectRules[props.projectId] ?? [])
const panel = computed(() => filterStore.getPanel(props.panelId))
const hasCurrentChips = computed(() => panel.value.chips.length > 0)

function resetForm() {
  editingRuleId.value = null
  savingFromPanel.value = false
  form.name = ''
  form.type = 'include'
  form.keywords = ''
  form.logic = 'or'
  form.enabled = true
}

function startNewRule() {
  resetForm()
}

function startEdit(rule: LogRule) {
  editingRuleId.value = rule.id
  savingFromPanel.value = false
  form.name = rule.name
  form.type = rule.type
  form.keywords = rule.keywords.join(', ')
  form.logic = rule.logic
  form.enabled = rule.enabled
}

function startSaveCurrentFilter() {
  resetForm()
  savingFromPanel.value = true
  const firstType = panel.value.chips[0]?.type ?? 'include'
  form.type = firstType
  form.logic = panel.value.logic
  form.keywords = panel.value.chips
    .filter(chip => chip.type === firstType)
    .map(chip => chip.keyword)
    .join(', ')
}

function splitKeywords(): string[] {
  return form.keywords.split(/[,;\t\n]+/).map(s => s.trim()).filter(Boolean)
}

async function saveRule() {
  if (savingFromPanel.value) {
    await filterStore.savePanelChipsAsRule(props.projectId, props.panelId, {
      name: form.name,
      type: form.type,
      logic: form.logic,
      enabled: form.enabled,
    })
    resetForm()
    return
  }
  const draft = {
    name: form.name,
    type: form.type,
    keywords: splitKeywords(),
    logic: form.logic,
    enabled: form.enabled,
  }
  if (editingRuleId.value) {
    await filterStore.updateRule(props.projectId, editingRuleId.value, draft)
  } else {
    await filterStore.createRule(props.projectId, draft)
  }
  resetForm()
}
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <section class="modal">
      <header class="modal-header">
        <div>
          <h2>过滤规则</h2>
          <p>项目级规则会保存到当前项目配置文件</p>
        </div>
        <button class="close-btn" title="关闭" @click="emit('close')">×</button>
      </header>

      <div class="modal-body">
        <div class="rule-list">
          <button
            v-for="rule in rules"
            :key="rule.id"
            class="rule-row"
            :class="{ disabled: !rule.enabled }"
            @click="startEdit(rule)"
          >
            <span class="rule-name">{{ rule.name }}</span>
            <span class="rule-meta">{{ rule.type === 'include' ? '包含' : '排除' }} · {{ rule.logic.toUpperCase() }}</span>
            <span class="rule-keywords">{{ rule.keywords.join(', ') }}</span>
          </button>
          <div v-if="rules.length === 0" class="empty-rules">暂无项目规则</div>
        </div>

        <form class="rule-form" @submit.prevent="saveRule">
          <div class="form-actions">
            <button type="button" data-test="new-rule" @click="startNewRule">新建规则</button>
            <button
              type="button"
              data-test="save-current-filter"
              :disabled="!hasCurrentChips"
              @click="startSaveCurrentFilter"
            >
              从当前过滤保存
            </button>
          </div>

          <label>
            名称
            <input data-test="rule-name" v-model="form.name" />
          </label>
          <label>
            关键词
            <textarea data-test="rule-keywords" v-model="form.keywords" placeholder="用逗号、分号或换行分隔" />
          </label>
          <div class="form-row">
            <label>
              类型
              <select v-model="form.type">
                <option value="include">包含</option>
                <option value="exclude">排除</option>
              </select>
            </label>
            <label>
              逻辑
              <select v-model="form.logic">
                <option value="or">OR</option>
                <option value="and">AND</option>
              </select>
            </label>
            <label class="enabled-check">
              <input type="checkbox" v-model="form.enabled" />
              启用
            </label>
          </div>

          <div class="form-footer">
            <button
              v-if="editingRuleId"
              type="button"
              class="danger"
              @click="filterStore.deleteRule(projectId, editingRuleId); resetForm()"
            >
              删除
            </button>
            <span class="spacer" />
            <button type="submit" data-test="save-rule" class="primary">保存</button>
          </div>
        </form>
      </div>
    </section>
  </div>
</template>

<style scoped>
.modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 1000;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  align-items: center;
  justify-content: center;
}
.modal {
  width: min(720px, calc(100vw - 40px));
  max-height: min(620px, calc(100vh - 40px));
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: 8px;
  display: flex;
  flex-direction: column;
}
.modal-header {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--border-secondary);
}
h2 { margin: 0; font-size: 15px; }
p { margin: 3px 0 0; font-size: 11px; color: var(--text-tertiary); }
.close-btn {
  background: transparent;
  border: none;
  color: var(--text-secondary);
  font-size: 18px;
  cursor: pointer;
}
.modal-body {
  display: grid;
  grid-template-columns: minmax(220px, 1fr) minmax(280px, 1.2fr);
  min-height: 380px;
  overflow: hidden;
}
.rule-list {
  border-right: 1px solid var(--border-secondary);
  overflow-y: auto;
  padding: 8px;
}
.rule-row {
  width: 100%;
  display: grid;
  grid-template-columns: 1fr;
  gap: 3px;
  text-align: left;
  padding: 9px 10px;
  border: 1px solid transparent;
  border-radius: 6px;
  background: transparent;
  color: var(--text-primary);
  cursor: pointer;
}
.rule-row:hover {
  background: var(--bg-overlay);
  border-color: var(--border);
}
.rule-row.disabled { opacity: 0.5; }
.rule-name { font-weight: 600; font-size: 12px; }
.rule-meta, .rule-keywords, .empty-rules {
  color: var(--text-tertiary);
  font-size: 10px;
}
.rule-form {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px;
}
.form-actions, .form-row, .form-footer {
  display: flex;
  align-items: center;
  gap: 8px;
}
label {
  display: flex;
  flex-direction: column;
  gap: 5px;
  color: var(--text-secondary);
  font-size: 11px;
}
input, textarea, select {
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 5px;
  color: var(--text-primary);
  padding: 6px 8px;
  font-size: 12px;
}
textarea { min-height: 86px; resize: vertical; }
button {
  border: 1px solid var(--border);
  border-radius: 5px;
  background: var(--bg-overlay);
  color: var(--text-secondary);
  padding: 5px 9px;
  cursor: pointer;
  font-size: 11px;
}
button:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
.enabled-check {
  flex-direction: row;
  align-items: center;
}
.enabled-check input { width: 13px; height: 13px; }
.spacer { flex: 1; }
.primary {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
}
.danger { color: var(--status-failed); }
</style>
```

- [ ] **Step 4: Wire modal from toolbar**

Modify `desktop/src/components/Panel/PanelToolbar.vue`:

- Import modal:

```ts
import RuleManagerModal from './RuleManagerModal.vue'
```

- Add state:

```ts
const showRules = ref(false)
```

- Replace the existing gear button:

```vue
<button
  class="rules-btn"
  title="管理过滤规则"
  :disabled="!projectId"
  @click="showRules = true"
>
  ⚙
</button>
<RuleManagerModal
  v-if="showRules && projectId"
  :project-id="projectId"
  :panel-id="panelId"
  @close="showRules = false"
/>
```

- Replace `.icon-btn` gear styling with a larger button:

```css
.rules-btn {
  width: 28px;
  height: 24px;
  border-radius: 5px;
  border: 1px solid var(--border);
  background: var(--bg-overlay);
  color: var(--text-secondary);
  cursor: pointer;
  flex-shrink: 0;
}
.rules-btn:hover:not(:disabled) {
  color: var(--text-primary);
  border-color: rgba(88, 166, 255, 0.45);
}
.rules-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
```

Update rule chip click to await the async store method:

```vue
@click="filterStore.toggleRule(projectId!, rule.id)"
```

No template change is required for awaiting in Vue event handlers.

- [ ] **Step 5: Run tests and build**

Run:

```bash
cd desktop
pnpm exec vitest run src/components/Panel/__tests__/RuleManagerModal.test.ts src/stores/__tests__/filter.test.ts
pnpm build
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add desktop/src/components/Panel/RuleManagerModal.vue desktop/src/components/Panel/__tests__/RuleManagerModal.test.ts desktop/src/components/Panel/PanelToolbar.vue
git commit -m "feat(desktop): add rule manager modal"
```

---

### Task 6: Settings Page And Route

**Files:**
- Create: `desktop/src/pages/SettingsPage.vue`
- Create: `desktop/src/pages/__tests__/SettingsPage.test.ts`
- Modify: `desktop/src/router/index.ts`
- Modify: `desktop/src/components/Sidebar/SidebarView.vue`

- [ ] **Step 1: Add failing settings page tests**

Create `desktop/src/pages/__tests__/SettingsPage.test.ts`:

```ts
/**
 * SettingsPage 测试桌面端设置页。
 *
 * 职责：
 *   - 验证通用设置展示和保存
 *   - 验证项目服务启动选择和显示隐藏操作
 *
 * 边界：
 *   - 不测试真实系统登录项
 *   - 不打开真实目录选择器
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import SettingsPage from '../SettingsPage.vue'
import { useAgentStore } from '@/stores/agent'
import { useSettingsStore } from '@/stores/settings'
import type { Project, Service } from '@/api/agent'

function service(id: string, name: string, required = false): Service {
  return {
    id,
    project_id: 'proj-1',
    name,
    status: '',
    command: 'pnpm dev',
    work_dir: '/tmp/project',
    required,
    order: 1,
  }
}

function project(services: Service[]): Project {
  return {
    id: 'proj-1',
    name: 'Project',
    root_path: '/tmp/project',
    services,
    selected_service_ids: ['worker'],
  }
}

describe('SettingsPage', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('通用页展示日志保留天数并保存', async () => {
    const settings = useSettingsStore()
    settings.agentSettings = { log_retention_days: 7 }
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'loadAutostart').mockResolvedValue(undefined)
    vi.spyOn(settings, 'saveLogRetentionDays').mockResolvedValue(undefined)

    const wrapper = mount(SettingsPage)
    await nextTick()
    const input = wrapper.find('[data-test="retention-days"]')
    await input.setValue(14)
    await input.trigger('change')

    expect(settings.saveLogRetentionDays).toHaveBeenCalledWith(14)
  })

  it('项目页可切换服务隐藏状态和启动选择', async () => {
    const api = service('svc-api', 'api', true)
    const worker = service('svc-worker', 'worker')
    const agent = useAgentStore()
    agent.projects = [project([api, worker])]
    vi.spyOn(agent, 'updateSelected').mockResolvedValue(undefined)
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'loadAutostart').mockResolvedValue(undefined)

    const wrapper = mount(SettingsPage)
    await wrapper.find('[data-test="settings-tab-projects"]').trigger('click')
    await wrapper.find('[data-test="toggle-hidden-svc-worker"]').trigger('click')
    await wrapper.find('[data-test="select-start-svc-worker"]').setValue(false)

    expect(settings.isServiceHidden('svc-worker')).toBe(true)
    expect(agent.updateSelected).toHaveBeenCalledWith('proj-1', ['api'])
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd desktop && pnpm exec vitest run src/pages/__tests__/SettingsPage.test.ts`

Expected: FAIL because `SettingsPage.vue` does not exist.

- [ ] **Step 3: Create settings page**

Create `desktop/src/pages/SettingsPage.vue`:

```vue
<!--
设置页

职责：
  - 展示和修改通用设置
  - 管理项目列表中的本地展示偏好和启动选择

边界：
  - 不处理 MCP 配置
  - 不直接启动或停止服务
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { open, message } from '@tauri-apps/plugin-dialog'
import { useAgentStore } from '@/stores/agent'
import { useSettingsStore } from '@/stores/settings'
import type { Project, Service } from '@/api/agent'

type SettingsTab = 'general' | 'projects'

const router = useRouter()
const agentStore = useAgentStore()
const settingsStore = useSettingsStore()
const selectedTab = ref<SettingsTab>('general')

onMounted(() => {
  void settingsStore.loadAgentSettings()
  void settingsStore.loadAutostart()
})

async function addProject() {
  const selected = await open({ directory: true, multiple: false, title: '选择项目根目录' })
  if (!selected || Array.isArray(selected)) return
  try {
    await agentStore.addProject(selected)
  } catch (e) {
    const msg = e instanceof Error ? e.message : '添加项目失败'
    await message(
      msg.includes('config') ? `${msg}\n请确认目录中有 .superdev/config.yaml` : msg,
      { title: '无法添加项目', kind: 'error' },
    )
  }
}

async function deleteProject(project: Project) {
  await agentStore.deleteProject(project.id)
}

function selectedStartNames(project: Project): string[] {
  const selected = new Set(project.selected_service_ids ?? [])
  for (const service of project.services) {
    if (service.required) selected.add(service.name)
  }
  return [...selected]
}

async function toggleStartSelection(project: Project, service: Service, checked: boolean) {
  if (service.required) return
  const selected = new Set(selectedStartNames(project))
  if (checked) selected.add(service.name)
  else selected.delete(service.name)
  await agentStore.updateSelected(project.id, [...selected])
}

function isSelectedForStart(project: Project, service: Service): boolean {
  if (service.required) return true
  return selectedStartNames(project).includes(service.name)
}

const retentionDays = computed({
  get: () => settingsStore.agentSettings.log_retention_days,
  set: value => {
    const days = Math.min(90, Math.max(1, Number(value)))
    void settingsStore.saveLogRetentionDays(days)
  },
})
</script>

<template>
  <div class="settings-page">
    <aside class="settings-sidebar">
      <button class="back-btn" @click="router.push('/')">← 返回</button>
      <button
        data-test="settings-tab-general"
        class="tab-btn"
        :class="{ active: selectedTab === 'general' }"
        @click="selectedTab = 'general'"
      >
        ⚙ 通用
      </button>
      <button
        data-test="settings-tab-projects"
        class="tab-btn"
        :class="{ active: selectedTab === 'projects' }"
        @click="selectedTab = 'projects'"
      >
        □ 项目
      </button>
    </aside>

    <main class="settings-main">
      <section v-if="selectedTab === 'general'" class="pane">
        <header class="pane-header">
          <h1>通用</h1>
        </header>
        <div class="setting-row">
          <div>
            <div class="setting-title">日志保留天数</div>
            <div class="setting-desc">超过此天数的日志会在 agent 启动时自动删除</div>
          </div>
          <input
            data-test="retention-days"
            class="number-input"
            type="number"
            min="1"
            max="90"
            :value="retentionDays"
            @change="retentionDays = Number(($event.target as HTMLInputElement).value)"
          />
        </div>
        <div class="setting-row">
          <div>
            <div class="setting-title">开机自启</div>
            <div class="setting-desc">登录系统后自动启动 SuperDev 桌面应用</div>
          </div>
          <label class="switch">
            <input
              type="checkbox"
              :checked="settingsStore.autostartEnabled"
              @change="settingsStore.setAutostart(($event.target as HTMLInputElement).checked)"
            />
            <span />
          </label>
        </div>
      </section>

      <section v-else class="pane">
        <header class="pane-header">
          <h1>项目</h1>
          <button class="primary-btn" @click="addProject">+ 添加项目</button>
        </header>
        <div class="project-list">
          <article v-for="project in agentStore.projects" :key="project.id" class="project-card">
            <header class="project-header">
              <div>
                <h2>{{ project.name }}</h2>
                <p>{{ project.root_path }}</p>
              </div>
              <div class="project-actions">
                <span>{{ project.services.length }} 个服务</span>
                <button class="danger-btn" @click="deleteProject(project)">删除</button>
              </div>
            </header>
            <div class="service-table">
              <div v-for="service in project.services" :key="service.id" class="service-row">
                <div>
                  <span class="service-name">{{ service.name }}</span>
                  <span v-if="service.required" class="required-badge">必选</span>
                </div>
                <label class="inline-check">
                  <input
                    :data-test="`select-start-${service.id}`"
                    type="checkbox"
                    :disabled="service.required"
                    :checked="isSelectedForStart(project, service)"
                    @change="toggleStartSelection(project, service, ($event.target as HTMLInputElement).checked)"
                  />
                  启动选中
                </label>
                <button
                  :data-test="`toggle-hidden-${service.id}`"
                  class="ghost-btn"
                  @click="settingsStore.toggleServiceHidden(service.id)"
                >
                  {{ settingsStore.isServiceHidden(service.id) ? '已隐藏' : '显示' }}
                </button>
              </div>
            </div>
          </article>
        </div>
      </section>
    </main>
  </div>
</template>

<style scoped>
.settings-page {
  display: flex;
  height: 100vh;
  background: var(--bg-primary);
  color: var(--text-primary);
}
.settings-sidebar {
  width: 160px;
  border-right: 1px solid var(--border-secondary);
  background: var(--bg-elevated);
  padding: 10px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.back-btn, .tab-btn {
  text-align: left;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--text-secondary);
  padding: 8px 10px;
  cursor: pointer;
}
.tab-btn.active {
  background: var(--bg-overlay);
  color: var(--text-primary);
}
.settings-main {
  flex: 1;
  overflow-y: auto;
}
.pane {
  max-width: 860px;
  padding: 22px;
}
.pane-header, .project-header, .setting-row, .service-row, .project-actions {
  display: flex;
  align-items: center;
}
.pane-header, .project-header, .setting-row {
  justify-content: space-between;
}
h1 { margin: 0 0 16px; font-size: 18px; }
h2 { margin: 0; font-size: 14px; }
p { margin: 4px 0 0; color: var(--text-tertiary); font-size: 11px; }
.setting-row, .project-card {
  border: 1px solid var(--border-secondary);
  background: var(--bg-elevated);
  border-radius: 8px;
}
.setting-row {
  padding: 14px 16px;
  margin-bottom: 10px;
}
.setting-title { font-size: 13px; font-weight: 600; }
.setting-desc { margin-top: 3px; color: var(--text-tertiary); font-size: 11px; }
.number-input {
  width: 72px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 5px;
  color: var(--text-primary);
  padding: 5px 7px;
}
.switch input { display: none; }
.switch span {
  width: 34px;
  height: 18px;
  border-radius: 999px;
  background: var(--border);
  display: block;
  position: relative;
}
.switch span::after {
  content: '';
  position: absolute;
  width: 14px;
  height: 14px;
  left: 2px;
  top: 2px;
  border-radius: 50%;
  background: var(--text-secondary);
  transition: transform 0.12s;
}
.switch input:checked + span {
  background: var(--accent);
}
.switch input:checked + span::after {
  transform: translateX(16px);
  background: #fff;
}
.primary-btn, .danger-btn, .ghost-btn {
  border-radius: 5px;
  border: 1px solid var(--border);
  padding: 5px 9px;
  cursor: pointer;
  font-size: 11px;
}
.primary-btn {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
}
.danger-btn {
  background: transparent;
  color: var(--status-failed);
}
.ghost-btn {
  background: var(--bg-overlay);
  color: var(--text-secondary);
}
.project-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.project-card {
  overflow: hidden;
}
.project-header {
  padding: 12px 14px;
  border-bottom: 1px solid var(--border-secondary);
}
.project-actions {
  gap: 10px;
  color: var(--text-tertiary);
  font-size: 11px;
}
.service-table {
  padding: 6px 10px 10px;
}
.service-row {
  justify-content: space-between;
  min-height: 32px;
  border-bottom: 1px solid var(--border-secondary);
}
.service-row:last-child {
  border-bottom: none;
}
.service-name { font-size: 12px; }
.required-badge {
  margin-left: 6px;
  color: var(--accent);
  font-size: 10px;
}
.inline-check {
  display: flex;
  align-items: center;
  gap: 5px;
  color: var(--text-secondary);
  font-size: 11px;
}
</style>
```

- [ ] **Step 4: Add route and sidebar entry**

Modify `desktop/src/router/index.ts`:

```ts
const SettingsPage = () => import('@/pages/SettingsPage.vue')
```

Add route:

```ts
{ path: '/settings', component: SettingsPage },
```

Modify `desktop/src/components/Sidebar/SidebarView.vue`:

```ts
import { useRouter } from 'vue-router'

const router = useRouter()
```

Add a settings entry above `.add-project`:

```vue
<div class="settings-entry" @click="router.push('/settings')">⚙ 设置</div>
```

Add style:

```css
.settings-entry {
  padding: 8px 12px;
  border-top: 1px solid var(--border-secondary);
  color: var(--text-tertiary);
  font-size: 11px;
  cursor: pointer;
}
.settings-entry:hover { color: var(--text-secondary); }
```

- [ ] **Step 5: Run tests and build**

Run:

```bash
cd desktop
pnpm exec vitest run src/pages/__tests__/SettingsPage.test.ts src/stores/__tests__/settings.test.ts
pnpm build
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add desktop/src/pages/SettingsPage.vue desktop/src/pages/__tests__/SettingsPage.test.ts desktop/src/router/index.ts desktop/src/components/Sidebar/SidebarView.vue
git commit -m "feat(desktop): add settings page"
```

---

### Task 7: Apply Hidden Services To Popover

**Files:**
- Modify: `desktop/src/components/Popover/PopoverProjectList.vue`
- Modify: `desktop/src/components/Popover/PopoverServicePanel.vue`
- Create: `desktop/src/components/Popover/__tests__/PopoverHiddenServices.test.ts`

- [ ] **Step 1: Add failing popover hidden service test**

Create `desktop/src/components/Popover/__tests__/PopoverHiddenServices.test.ts`:

```ts
/**
 * Popover hidden services 测试本地显示/隐藏偏好。
 *
 * 职责：
 *   - 验证 popover 左侧项目服务列表隐藏被设置页隐藏的服务
 *   - 验证 popover 右侧服务控制面板隐藏被设置页隐藏的服务
 *
 * 边界：
 *   - 不测试真实托盘窗口
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import PopoverProjectList from '../PopoverProjectList.vue'
import PopoverServicePanel from '../PopoverServicePanel.vue'
import { useAgentStore } from '@/stores/agent'
import { useSettingsStore } from '@/stores/settings'
import type { Project, Service } from '@/api/agent'

function service(id: string, name: string): Service {
  return {
    id,
    project_id: 'proj-1',
    name,
    status: '',
    command: 'pnpm dev',
    work_dir: '/tmp/project',
    required: false,
    order: 1,
  }
}

function project(services: Service[]): Project {
  return {
    id: 'proj-1',
    name: 'Project',
    root_path: '/tmp/project',
    services,
    selected_service_ids: [],
  }
}

describe('popover hidden services', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('隐藏服务不出现在 popover 列表和控制面板', () => {
    const api = service('svc-api', 'api')
    const worker = service('svc-worker', 'worker')
    const agent = useAgentStore()
    agent.projects = [project([api, worker])]
    const settings = useSettingsStore()
    settings.toggleServiceHidden('svc-worker')

    const list = mount(PopoverProjectList)
    const panel = mount(PopoverServicePanel, { props: { project: agent.projects[0] } })

    expect(list.text()).toContain('api')
    expect(list.text()).not.toContain('worker')
    expect(panel.text()).toContain('api')
    expect(panel.text()).not.toContain('worker')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd desktop && pnpm exec vitest run src/components/Popover/__tests__/PopoverHiddenServices.test.ts`

Expected: FAIL because popover components do not use `settingsStore`.

- [ ] **Step 3: Filter hidden services in popover left list**

Modify `desktop/src/components/Popover/PopoverProjectList.vue`:

```ts
import { useSettingsStore } from '@/stores/settings'

const settingsStore = useSettingsStore()

function visibleServices(project: Project) {
  return project.services.filter(s => !settingsStore.isServiceHidden(s.id))
}

function filteredServices(project: Project) {
  const services = visibleServices(project)
  if (!searchText.value) return services
  return services.filter(s =>
    s.name.toLowerCase().includes(searchText.value.toLowerCase())
  )
}
```

Leave `projectStatusColor` based on all services so hidden services do not hide true project health.

- [ ] **Step 4: Filter hidden services in popover control panel**

Modify `desktop/src/components/Popover/PopoverServicePanel.vue`:

```ts
import { useSettingsStore } from '@/stores/settings'

const settingsStore = useSettingsStore()

const visibleServices = computed(() =>
  props.project.services.filter(s => !settingsStore.isServiceHidden(s.id))
)
const requiredServices = computed(() =>
  visibleServices.value.filter(s => s.required)
)
const optionalServices = computed(() =>
  visibleServices.value.filter(s => !s.required)
)
const runningCount = computed(() =>
  visibleServices.value.filter(s => s.status === 'running').length
)
const startingCount = computed(() =>
  visibleServices.value.filter(s => s.status === 'starting').length
)
const stoppedCount = computed(() =>
  visibleServices.value.filter(s => s.status !== 'running' && s.status !== 'starting').length
)
const selectedServices = computed(() =>
  visibleServices.value.filter(s =>
    agentStore.isServiceSelectedForStart(props.project.id, s.name)
  )
)
```

Update `toggleSelectAll` to use `visibleServices.value`:

```ts
const all = visibleServices.value.map(s => s.name)
```

- [ ] **Step 5: Run tests**

Run: `cd desktop && pnpm exec vitest run src/components/Popover/__tests__/PopoverHiddenServices.test.ts`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add desktop/src/components/Popover/PopoverProjectList.vue desktop/src/components/Popover/PopoverServicePanel.vue desktop/src/components/Popover/__tests__/PopoverHiddenServices.test.ts
git commit -m "feat(desktop): hide popover services"
```

---

### Task 8: Full Verification And Polish

**Files:**
- Modify only files already touched if verification reveals issues.

- [ ] **Step 1: Run full frontend tests**

Run: `cd desktop && pnpm exec vitest run`

Expected: PASS.

- [ ] **Step 2: Run desktop build**

Run: `cd desktop && pnpm build`

Expected: PASS.

- [ ] **Step 3: Run full agent tests**

Run: `cd agent && go test ./...`

Expected: PASS.

- [ ] **Step 4: Inspect final diff**

Run: `git status --short`

Expected: only intended files are modified. The pre-existing `desktop/src-tauri/binaries/superdev-agent-aarch64-apple-darwin` modification may still appear; do not stage or revert it unless the user asks.

- [ ] **Step 5: Finish verification state**

Run: `git status --short`

Expected: no new verification-only edits. If verification revealed a defect, return to the task that introduced that defect, add a concrete test for it, fix it there, and commit the exact files owned by that task using that task's commit style. Do not stage or revert `desktop/src-tauri/binaries/superdev-agent-aarch64-apple-darwin`.
