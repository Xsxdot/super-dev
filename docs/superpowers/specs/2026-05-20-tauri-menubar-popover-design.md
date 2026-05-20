# Tauri Desktop 菜单栏 Popover 设计

**日期**：2026-05-20  
**范围**：`desktop/` Tauri 应用

---

## 背景

Swift 原生版本（`SuperDev/SuperDev/UI/MenuBar/`）已有完整的 NSPopover 工作流：单击菜单栏图标弹出 Popover，展示项目/服务列表，可搜索、勾选、批量启动，无需打开主窗口。

Tauri Desktop 版本的系统托盘目前只有"显示主窗口 / 退出"两项，缺少等价的快速控制能力。本文档描述如何在 Tauri 版本中补齐该功能。

---

## 目标

- 单击托盘图标 → 弹出 Popover 窗口（贴近图标，精确定位）
- 右键点击托盘图标 → 弹出菜单（设置… / 退出）
- Popover 内：左栏项目列表 + 悬停右栏服务面板，含搜索、勾选、批量启动、单服务启停/重启
- 失焦自动关闭 Popover，不影响主窗口

---

## 方案选择

采用**方案 A：独立 WebviewWindow + Vue Popover 路由**。

- 新建 `WebviewWindow`（label: `popover`），加载同一 Vue 应用的 `/popover` 路由
- 复用已有 `agentStore` 和 API 层，独立轮询
- 放弃方案 B（主窗口浮层，体验差）和方案 C（独立 HTML 入口，维护成本高）

---

## 架构

```
托盘图标
  左键单击 → Rust: 获取 tray.rect() → 计算窗口坐标 → 创建/显示 popover 窗口
  右键单击 → Rust: 弹出 NSMenu（设置… / 退出）
  失焦       → Rust: on_window_event(Focused(false)) → 隐藏 popover 窗口

popover 窗口（label: "popover"）
  - 无边框、alwaysOnTop、skipTaskbar
  - 尺寸：440 × 420
  - 路由：/#/popover

Vue /popover → PopoverPage.vue
  - agentStore（独立轮询，窗口可见时 start，隐藏时 stop）
  - 不引入 panelStore / logStore
```

---

## 组件结构

```
src/
  router/index.ts
  pages/
    PopoverPage.vue                      ← 根组件，双栏布局
  components/
    Popover/
      PopoverProjectList.vue             ← 左栏：项目列表 + 搜索
      PopoverServicePanel.vue            ← 右栏：header + toolbar + 服务列表 + footer
      PopoverServiceRow.vue              ← 服务行：checkbox + 状态点 + 启停/重启
```

**复用**：`agentStore`、`api/agent.ts`、CSS 变量 token  
**不复用**：`SidebarView.vue`（绑定 panelStore）、`ServiceRow.vue`（语义不同）

---

## Rust 实现

### 窗口创建与定位

```
左键单击：
1. popover 窗口已可见 → 隐藏（toggle）
2. 不存在 → 创建：
     decorations: false, always_on_top: true, skip_taskbar: true, visible: false
3. tray_icon.rect() → Rect
4. x = rect.left + rect.width/2 - 220   （窗口宽 440，居中对齐图标）
5. y = rect.bottom + 8
6. window.set_position() → window.show() → window.set_focus()
```

### 失焦关闭

```
on_window_event("popover", Focused(false)) → window.hide()
```

### 右键菜单

保持现有行为：弹出 NSMenu，含"设置…"（show 主窗口）和"退出 SuperDev"。

### tray.rect() fallback

若返回 `None`（极少数情况），fallback 到屏幕右上角固定偏移：
```
x = screen_width - 460
y = 30
```

### 屏幕边界检测

若 `y + 420 > screen_height`（Dock 在下方时窗口超出屏幕），将窗口显示在图标上方：
```
y = rect.top - 420 - 8
```

---

## 数据流

### 多窗口隔离

Tauri 多窗口每个 WebviewWindow 是独立 WebView 进程，不共享 Pinia store。Popover 窗口有自己的 Vue 实例，需独立轮询。

### 轮询策略

```
PopoverPage onMounted  → agentStore.startPolling()
PopoverPage onUnmounted → agentStore.stopPolling()
```

实际控制由 Rust 侧 show/hide 驱动，Vue 侧通过 Tauri event 或 `document.visibilitychange` 响应。

### 操作后立即刷新

```
startService / stopService / restartService 后
  → agentStore.refreshServices()（不等 2s 轮询间隔）
```

### 勾选持久化

```
勾选变更 → agentStore.updateSelected(projectId, names)
           → PUT /api/projects/:id/selected
启动选中 → agentStore.startSelected(projectId)
           → POST /api/projects/:id/start-selected
```

---

## 边界情况

| 情况 | 处理 |
|------|------|
| agent 未连接 | 显示"未连接"占位，不渲染服务列表 |
| tray.rect() 返回 None | fallback 到屏幕右上角固定坐标 |
| 窗口超出屏幕底部 | 显示在图标上方 |
| 快速连续点击托盘 | toggle 逻辑防止重复创建 |

---

## 文件变更清单

### 新增

```
desktop/src/pages/PopoverPage.vue
desktop/src/components/Popover/PopoverProjectList.vue
desktop/src/components/Popover/PopoverServicePanel.vue
desktop/src/components/Popover/PopoverServiceRow.vue
```

### 修改

```
desktop/src/router/index.ts
desktop/src/App.vue                          （按路由区分初始化逻辑）
desktop/src-tauri/src/main.rs                （托盘事件、popover 窗口）
desktop/src-tauri/capabilities/default.json  （windows 加入 "popover"）
desktop/src-tauri/tauri.conf.json            （可选：预声明 popover 窗口）
```

### 不需要改动

```
desktop/src/stores/agent.ts
desktop/src/api/agent.ts
agent/ (Go 后端)
```
