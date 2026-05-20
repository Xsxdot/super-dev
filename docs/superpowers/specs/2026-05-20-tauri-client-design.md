# SuperDev Tauri 桌面客户端设计文档

**日期**：2026-05-20  
**状态**：已确认，待实现

---

## 背景与目标

将 SuperDev 的 macOS Swift/SwiftUI 客户端重写为跨平台 Tauri 桌面应用，复用已实现的 Go agent HTTP/WebSocket API，前端用 Vue 3 实现与原版功能对等的 UI。

---

## 技术栈

| 层 | 技术 |
|---|---|
| 桌面壳 | Tauri 2（Rust） |
| 前端框架 | Vue 3 + TypeScript |
| 状态管理 | Pinia |
| UI 组件库 | shadcn-vue + Tailwind CSS |
| HTTP 客户端 | fetch（原生） |
| 构建工具 | Vite |

---

## Agent 生命周期

- Tauri 将 `superdev-agent` 二进制作为 sidecar 打包（`tauri.conf.json` `externalBin` 声明）
- 主窗口启动时，Rust `agent.rs` 用 `tauri::api::process::Command` 拉起 agent，参数：`--addr :27017 --data ~/.superdev`
- `tauri::RunEvent::ExitRequested` 时 kill agent 进程
- 前端固定连接 `http://localhost:27017`，右下角显示 agent 连接状态（绿点/红点）

---

## 目录结构

```
desktop/
├── src-tauri/
│   ├── src/
│   │   ├── main.rs       # Tauri 入口，注册命令和事件
│   │   └── agent.rs      # agent 进程生命周期管理（spawn/kill）
│   └── tauri.conf.json   # sidecar、窗口、托盘配置
└── src/
    ├── main.ts
    ├── App.vue
    ├── components/
    │   ├── Sidebar/
    │   │   ├── SidebarView.vue     # 项目/服务列表容器
    │   │   ├── ProjectHeader.vue   # 项目标题行（▶ ⏹ icon 按钮）
    │   │   └── ServiceRow.vue      # 服务行（checkbox + 状态圆点 + 悬停操作按钮）
    │   ├── Panel/
    │   │   ├── PanelLayout.vue     # 递归分栏布局树渲染
    │   │   ├── PanelLeaf.vue       # 叶子面板（header + LogPanel + 拖放覆盖层）
    │   │   ├── LogPanel.vue        # 日志面板主体（工具栏 + 日志列表 + 状态栏）
    │   │   ├── PanelToolbar.vue    # 工具栏（过滤 chip + 历史 + 规则 + 单面板书签）
    │   │   └── LogRow.vue          # 单行日志渲染
    │   └── BottomBar.vue           # 底部面板操作栏
    ├── stores/
    │   ├── agent.ts      # 服务状态轮询（GET /api/projects, /api/services）
    │   ├── log.ts        # 每个 serviceId 的日志缓冲 + WebSocket 连接管理
    │   ├── panel.ts      # 面板布局树、焦点状态、每个面板的 scope
    │   ├── bookmark.ts   # 书签和同步组纯客户端状态
    │   └── filter.ts     # 每个面板的 chip 状态 + LogRule 缓存
    ├── composables/
    │   ├── useWebSocket.ts   # 单个 serviceId 的 WS 连接封装
    │   └── useDragDrop.ts    # 面板拖放分栏逻辑
    └── api/
        └── agent.ts      # HTTP 请求封装（fetch）
```

---

## 主窗口布局

```
┌─────────────────────────────────────────────────────┐
│  标题栏（macOS 红绿灯 + "SuperDev"）                  │
├──────────────┬──────────────────────────────────────┤
│              │  面板区（自由分栏，递归布局树）           │
│  侧边栏       │  ┌───────────────┬─────────────────┐ │
│  160–200px   │  │ 面板 header   │ 面板 header      │ │
│              │  ├───────────────┼─────────────────┤ │
│  项目一  ▶ ⏹ │  │ 工具栏        │ 工具栏           │ │
│  ☑ ● gateway │  ├───────────────┼─────────────────┤ │
│  ☑ ● user-svc│  │ 日志列表      │ 日志列表         │ │
│  ☐ ○ order   │  │               │                  │ │
│              │  ├───────────────┼─────────────────┤ │
│  项目二  ▶ ⏹ │  │ 状态栏        │ 状态栏           │ │
│  ☐ ● api     │  └───────────────┴─────────────────┘ │
│  ☐ ○ worker  │                                       │
│              ├──────────────────────────────────────┤
│  + 添加项目  │  底部面板操作栏                        │
└──────────────┴──────────────────────────────────────┘
```

最小窗口尺寸：800×500。

---

## 侧边栏

### 服务行

每行包含：
1. **Checkbox**：控制该服务是否纳入"启动选中"范围，状态持久化到 agent（`selected_service_ids`）
2. **状态圆点**：颜色对应服务状态（绿=running、黄=starting、灰=stopped、红=failed）
3. **服务名**：单行截断
4. **悬停操作按钮**（鼠标移入时从右侧淡入，移出时淡出）：
   - 运行中：↺ 重启 + ⏹ 停止
   - 已停止/失败：▶ 启动

### 项目标题行

项目名左对齐，右侧两个 icon 按钮：
- **▶**（启动选中）：启动该项目下所有已勾选服务，按 `order` 分组串行启动
- **⏹**（全部停止）：停止该项目下所有运行中服务

### 底部

"+ 添加项目"入口：打开文件夹选择对话框，选择包含 `.superdev/config.yaml` 的项目根目录，调用 `POST /api/projects`。

---

## 面板布局系统

### 数据结构

```typescript
type PanelNode =
  | { type: 'leaf'; id: string; serviceId: string | null; projectId: string | null }
  | { type: 'split'; id: string; axis: 'h' | 'v'; ratio: number; first: PanelNode; second: PanelNode }
```

`panelStore` 维护根节点，`PanelLayout.vue` 递归渲染。

### 拖放分栏

- 侧边栏服务行可拖拽，携带 `serviceId`
- 拖入面板时：落在中央 60% 区域 → 替换当前面板服务；落在四边 20% 区域 → 对应方向分栏
- 分栏时将当前叶子节点替换为 split 节点，新叶子承载拖入的服务
- 拖放时面板边缘显示高亮覆盖层提示

### 面板 header

显示服务名（或"未选择"），右侧有关闭按钮（根节点不显示，分栏后才显示）。点击面板任意区域设置焦点。

---

## 日志面板（LogPanel）

### 工具栏（PanelToolbar）

从左到右：
1. **过滤区**
   - 包含/排除 segmented picker
   - 关键词输入框（回车添加 chip，支持逗号/分号批量输入）
   - Chip 列表（点击 +/− 切换类型，点击 ✕ 删除）
   - AND/OR 切换按钮（多 chip 时显示）
   - 分隔线
   - 项目级 LogRule chip 快捷开关（点击 toggle enabled，有规则时显示）
2. **操作区**（右侧）
   - 🕐 历史记录下拉菜单（列出该服务的历史 runId，点击切换到历史模式）
   - ⚙ 持久化规则管理（打开规则列表 sheet）
   - ↓ 保存为规则（有 chip 时显示，将当前 chip 保存为项目 LogRule）
3. **书签区**（最右，单面板独立书签）
   - 空闲：⏺ 绿色，点击开始录制
   - 录制中：红色"● N 条" + ⏹ 停止
   - 完成：条数 + ⎘ 复制 + ↑ 导出 + ✕ 清除，再次点击 ⏺ 重新开始

### 历史模式

切换到历史 runId 后：
- 日志列表只显示该 run 的日志（调用 `GET /api/logs?service={id}&run={runId}`）
- 工具栏上方出现黄色历史 banner："查看历史记录：MM/DD HH:mm · N 条" + "返回实时"按钮
- 返回实时时重新接入 WebSocket 流

### 日志列表

- 虚拟滚动（`@tanstack/vue-virtual` 或手写简单版），避免大量日志卡顿
- 每行：时间戳（HH:mm:ss）+ [服务名]（颜色按服务名 hash）+ LEVEL + 消息
- ERROR 行背景红色淡色，WARN 行背景黄色淡色
- 书签录制中：startTime 之后的行背景加深标记
- 书签完成：start/end 之间插入"▶ 开始标记"/"■ 结束标记"分隔行
- 自动跟随底部：用户滚动离开底部后右下角出现"↓ N 条新日志"浮动按钮，点击回底部

### 状态栏

左侧：模式（实时/历史） + 显示条数  
右侧：错误数 badge（红）、警告数 badge（黄）

---

## 底部面板操作栏

聚合当前所有面板中的服务（去重），提供快捷操作：

```
面板服务  ☑ ● gateway  ☑ ● user-svc  |  ↺ 重启  ⏹ 停止  |  ☐ 同步录制  ⏺  |  ● localhost:27017
```

- **服务列表**：从 `panelStore` 取所有叶子面板的 serviceId，去重后显示，每个可独立勾选
- **批量操作**：对勾选的服务执行重启/停止
- **同步录制**：勾选"同步录制" checkbox 后，⏺ 按钮同时对所有勾选服务对应的面板开始/停止书签录制，时间对齐
- **agent 状态**：右侧绿/红圆点 + `localhost:27017`

---

## 数据流与状态管理

### agentStore

- 启动时调用 `GET /api/projects`，填充项目和服务列表
- 每 2 秒调用 `GET /api/services`（按 projectId 过滤），更新服务状态和 PID
- 暴露：`projects[]`、`services(projectId)`、`serviceStatus(serviceId)`

### logStore

- 按 `serviceId` 维护日志数组和 WebSocket 连接
- 同一 `serviceId` 多个面板共享一个 WS 连接（引用计数）
- 面板 mount 时 `subscribe(serviceId, panelId)`，unmount 时 `unsubscribe(serviceId, panelId)`
- 引用计数归零时关闭 WS 连接
- WS 连接后先接收历史 200 条，之后实时追加
- 历史模式：调用 `GET /api/logs?service={id}&run={runId}&limit=1000`，存入独立的 `historyLogs` map

### panelStore

- 维护 `PanelNode` 根节点树和 `focusedPanelId`
- 提供：`allLeafPanels()`、`splitLeaf()`、`replaceScope()`、`removeLeaf()`
- 每次变更后序列化到 `localStorage` 持久化布局

### filterStore

- 按 `panelId` 维护 chip 列表、chipLogic（AND/OR）、当前 include/exclude 模式选择
- 按 `projectId` 缓存 LogRule 列表（从 `GET /api/projects/{id}/rules` 加载）
- 暴露 `filteredLogs(panelId, rawLogs[])` 计算函数

### bookmarkStore

- 按 `panelId` 维护 `Bookmark { startTime, endTime, lockedLogs[], state: idle|recording|done }`
- 录制中每次 logStore 新增日志时，将过滤后日志追加到对应面板的 `lockedLogs`
- 同步组：`syncPanelIds: Set<string>`，开始/停止同步录制时批量操作所有面板
- 导出：将 `lockedLogs` 格式化为文本，调用 Tauri `dialog.save()` 保存文件

---

## 系统托盘

- Tauri 托盘图标（macOS menubar、Windows 系统托盘）
- 右键菜单：
  - 显示主窗口
  - 退出（kill agent + 关闭窗口）

---

## LogRule 管理

- 点击 ⚙ 规则按钮打开 sheet（覆盖层），列出当前项目的所有 LogRule
- 支持：创建（名称 + 类型 + 关键词 + 逻辑）、启用/禁用 toggle、删除
- 保存时调用 `PUT /api/projects/{id}/rules`
- 保存为规则（chip → rule）：弹出小 dialog 输入规则名，调用同接口

---

## 过滤逻辑（客户端）

```
rawLogs
  → 应用 enabled LogRules（项目级，持久化）
      include rules: 保留匹配任一规则 keywords 的行（AND/OR 由 rule.logic 决定）
      exclude rules: 删除匹配任一规则 keywords 的行
  → 应用 chips（面板临时，不持久化）
      include chips: 保留包含任一 keyword 的行（chipLogic AND/OR）
      exclude chips: 删除包含任一 keyword 的行
  → 输出 filteredLogs
```

---

## 扩展点预留

- `api/agent.ts` 中 baseURL 可配置，为未来连接远程 agent 留口
- `agentStore` 预留 `agentUrl` 字段，Settings 页可修改（v2）

---

## 现有功能迁移对照

| Swift 版功能 | Vue 客户端对应实现 |
|---|---|
| `PanelLayout`（递归分栏） | `PanelLayout.vue` 递归渲染 `PanelNode` 树 |
| `SidebarView`（拖拽服务） | `SidebarView.vue` + `useDragDrop.ts` |
| `LogPanelView`（过滤 chip） | `LogPanel.vue` + `filterStore` |
| `LogRule` 项目级规则 | `filterStore` + `PUT /api/projects/{id}/rules` |
| `LogBookmark`（单面板书签） | `bookmarkStore` 按 panelId，面板工具栏控制 |
| `SyncGroup`（多面板同步录制） | `bookmarkStore.syncPanelIds`，底部栏统一控制 |
| `AppCore.returnToLiveLogs()` | `logStore` 切回实时 WS 模式 |
| `UserDefaults` 布局持久化 | `panelStore` 序列化到 localStorage |
| `NSSavePanel` 导出 | Tauri `dialog.save()` |
