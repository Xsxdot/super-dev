# PopoverView 样式重设计

## 背景与目标

SuperDev 是一个面向开发者的 macOS menubar 服务管理工具，目标作为开源项目发布。当前 PopoverView 功能正常，但视觉上信息层次不清、按钮拥挤、缺乏产品感，需要全面重设计以提升开源吸引力。

## 设计方向

**Pro Dark × Command Palette**：参考 GitHub 深色主题的配色体系，采用双面板布局，左侧为带搜索的项目/服务导航，右侧为服务控制面板。

## 配色规范

| 用途 | 色值 |
|------|------|
| 窗口背景（左侧） | `#0d1117` |
| 窗口背景（右侧） | `#010409` |
| 选中行 / 次级背景 | `#161b22` |
| 分割线 / 边框 | `#21262d` |
| 次级边框 | `#30363d` |
| 主文字 | `#e6edf3` |
| 次文字 | `#8b949e` |
| 弱文字 | `#6e7681` |
| 强调蓝（选中条、按钮） | `#1f6feb` |
| 运行绿 | `#3fb950` |
| 启动黄 | `#d29922` |
| 错误红 | `#f85149` |

## 布局规格

```
┌─────────────────────┬──────────────────────────────┐
│   左侧 170px        │   右侧 260px                  │
│ ┌─────────────────┐ │ ┌──────────────────────────┐  │
│ │ 搜索栏          │ │ │ 项目名 + 状态badge + 按钮 │  │
│ ├─────────────────┤ │ ├──────────────────────────┤  │
│ │ PROJECT LABEL ● │ │ │ 全选 | 反选  [全停][启动] │  │
│ │ ● api-server 运行│ │ ├──────────────────────────┤  │
│ │   worker     运行│ │ │ 必须启动                  │  │
│ │   scheduler  启动│ │ │ ☑ ● api-server  运行 [■] │  │
│ │   redis      停止│ │ │ ☑ ● worker      运行 [■] │  │
│ ├─────────────────┤ │ │ 可选                      │  │
│ │ + 添加项目      │ │ │ □ ● scheduler   启动 [■] │  │
│ └─────────────────┘ │ │ □ ● redis       停止 [▶] │  │
│                     │ ├──────────────────────────┤  │
│                     │ │             查看日志 →    │  │
│                     │ └──────────────────────────┘  │
└─────────────────────┴──────────────────────────────┘
总宽：430px（无项目时 170px），最小高度：300px
```

## 各区域设计细节

### 左侧面板（170px）

**搜索栏**
- 背景 `#161b22`，边框 `#30363d`，圆角 6px
- placeholder 文字：「搜索服务…」，颜色 `#6e7681`
- 左侧 SF Symbol `magnifyingglass` 或自绘 SVG

**项目 label**
- 9px uppercase，letter-spacing 0.8px，颜色 `#6e7681`
- 右侧跟随项目整体状态小圆点（同服务状态色）

**服务行**
- 高度：每行 ~28px（padding vertical 5px）
- 左侧状态点：7×7px 圆形，运行/启动中加 `box-shadow: 0 0 5px <color>66` glow
- 服务名：选中行 `#e6edf3`，其余 `#8b949e`
- 右侧状态文字：9px，颜色跟随状态
- 选中行：背景 `#161b22`，左侧 2px `#1f6feb` 竖条

**底部**
- 「+ 添加项目」颜色 `#1f6feb`，10px

### 右侧面板（260px）

**Header**
- 项目名：13px semibold，`#e6edf3`
- 「全停」按钮：背景 `#21262d`，边框 `#30363d`，文字 `#8b949e`，圆角 5px
- 「▶ 启动选中」按钮：背景 `#1f6feb`，文字 `#fff`，圆角 5px
- 状态 badge 行：`background: <color>18; border: 1px solid <color>33`，圆角 4px，9px 文字

**Toolbar 行**
- 背景 `#0d1117`，border-bottom `#161b22`
- 全选 checkbox：13×13px 圆角方块，已选背景 `#1f6feb`，内有白色矩形（indeterminate 状态用横线）
- 「反选」：plain 按钮，`#8b949e`
- 操作按钮组移至 header，toolbar 行只保留全选/反选

**服务行**
- Checkbox：13×13px 圆角 3px；选中背景 `#1f6feb`，未选中 border `#30363d`
- 状态点：7×7px，运行/启动中加 glow
- 启停按钮：18×18px 圆角 3px
  - 运行中：背景 `#3fb950`，图标 `■`（停止），文字色 `#000`
  - 启动中：背景 `#d29922`，图标 `■`，文字色 `#000`
  - 已停止：背景 `#21262d`，边框 `#30363d`，图标 `▶`，文字色 `#8b949e`
- 分组 label：9px uppercase，`#6e7681`

**Footer**
- 右对齐「查看日志」，颜色 `#1f6feb`，10px，左侧小图标

## 实现要点

1. `PopoverView` 整体不依赖系统 `List`，改用 `ScrollView + LazyVStack` 手动布局，以获得精确的间距控制
2. 状态点 glow 效果用 SwiftUI `.shadow(color:radius:)` 实现
3. 左侧搜索使用 `TextField` with `.textFieldStyle(.plain)`，外部包 `HStack` 自绘边框
4. 项目 hover 逻辑保持不变（`onHover`），选中态用 `hoveredProjectId` 驱动左侧高亮
5. 按钮尺寸统一用 `.controlSize(.small)` + `.buttonStyle(.plain)` 自绘，避免系统按钮样式干扰
6. 颜色常量提取到一个 `Theme` 枚举，方便后续整体主题切换

## 影响范围

- 修改文件：`SuperDev/UI/MenuBar/PopoverView.swift`
- 新增文件：可选，如提取 `Theme.swift` 颜色常量
- 不影响：逻辑层（AppCore、ProcessManager）、MainWindowView、SettingsView
