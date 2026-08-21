# 原型站说明

真实桌面端（`desktop/`）的形态镜像基准站。页面清单、导航结构、布局骨架与设计 token 随真实前端演进持续同步；按功能做 UX 走查时从本目录 fork 分支副本（见 `prototyping-in-brainstorm` skill），不直接改 base。

生成方式：扫描 `desktop/src/router/index.ts` 路由表 + 组件结构（2026-08-01 首次生成）。节点中心不是独立路由，是 MainPage 工作台内打开的视图，原型中单独成页表达。

| 页面 | 对应功能 | 来源路由 | 确认状态 |
|------|---------|---------|---------|
| index.html | 主界面（侧边栏 + 日志工作台 + 底栏） | / (MainPage) | 未确认 |
| pages/node-center.html | 节点中心（主机与远端节点卡片） | / 内工作台视图 (NodeCenterView) | 未确认 |
| pages/settings.html | 设置（通用/项目/主机/Agent/审批） | /settings | 未确认 |
| pages/project-overview.html | 项目概览（运行矩阵/流水线/入口） | /project/:id/overview | 未确认 |
| pages/onboarding.html | 首次引导 | /onboarding | 未确认 |
| pages/popover.html | 菜单栏审批弹层 | /popover | 未确认 |

已知有损转译：日志面板为静态占位（真实为虚拟列表 + 跟随贴底）；设置页导航项是真实 tab 集合的代表性子集（真实还有 DNS/证书/调试浏览器/MCP/模板等 tab）；工作区分屏/拖拽布局未表达。

## 功能走查确认记录

| 功能 | 走查载体（fork） | 涉及页面 | 确认状态 |
|------|----------------|---------|---------|
| 远程开发机 · 服务行归属+镜像状态标注 | remote-dev-machine | index.html | 确认中（2026-08-01 用户走查认可） |
| 远程开发机 · 端口镜像冲突态（不自动换端口+占用详情弹窗） | remote-dev-machine | index.html | 确认中（2026-08-01 用户走查认可） |
| 远程开发机 · 双面孔节点卡（桌面端在线徽标+端口镜像区） | remote-dev-machine | pages/node-center.html | 确认中（2026-08-01 用户走查认可） |
| 远程开发机 · 双控制面审批（先裁决者生效+对方已处理态） | remote-dev-machine | pages/popover.html | 确认中（2026-08-01 用户走查认可） |
| 远程开发机 · 配置面（主机级「开发机模式」开关+纳管已有 agent；添加时不默认勾选） | remote-dev-machine | pages/settings.html | 确认中（2026-08-01 用户裁决：主机级粒度够、不默认勾选） |
| AI 临时库供给 · 设置「数据源」页（管理连接登记+权限探测+db 号占用图+活跃临时资源+对账） | db-provisioning | pages/settings.html | 确认中（2026-08-21 用户走查认可） |
| AI 临时库供给 · 项目配置「数据源」区块（绑定实例/开发库、克隆前踢连接开关、配额与 TTL、试跑） | db-provisioning | pages/project-datasource.html（新增页，真实对应 ProjectConfigEditor 新区块） | 确认中（2026-08-21 用户走查认可） |
