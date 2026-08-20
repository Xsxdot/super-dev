# 端口镜像帧口径重定义执行记录

## 执行记录

- Task 1 提交：`5b10fb57`。新增 `ManagedDeployment.Ports`，补齐下发与远端合成传递；两条端口回归测试先以缺字段编译失败，补实现后通过；`go test ./api/ -count=1` 通过。
- Task 2 提交：`dec9f07b`。节点帧改为遍历全部已注册项目，并保留归属判断在控制面过滤侧。检查到的 `managedProjectsSnapshot` 调用点只有 `agent/api/handler_node_status.go` 的节点帧组装处，已替换为 `localProjectsSnapshot`。Task 2 Step 5 的 `TestManagedRuntimeInstances|TestNodeStatus|TestManagedDeployments` 全部通过。
- Task 2 旧测试更新：仅更新 `TestNodeStatusSnapshotCarriesPortsAndStoppedInstances` 的过时注释；Task 1 已使 `ManagedDeployment` 与合成逻辑透传 Ports，注释原先声称“不透传”已不成立，测试断言未改变。
- Task 3 提交：`f9936a23`。`computeExpected` 接收本控制面已知 deployment 集合，`nil` 保持不过滤；生产闭包每轮从 `app.projects` 现取集合。过滤日志使用 `Manager.filteredOnce` 跨轮记忆，并以 `log.Printf` 对每个 deployment id 只记录一次；`KnownDeployments == nil` 时在 `NewManager` 打可见性日志。`go test ./portmirror/ -v` 全部通过。
- Task 4 全量回归：`go test ./... -count=1` 未通过。普通沙箱运行失败包为 `agent/process`、`agent/sshkeys`、`agent/tunnel`；逐包复跑的失败为 `process: TestProbeReadyTimeout`，`sshkeys: TestScanSelectsOnlyPrivateKeys`、`TestScanSkipsUnreadableFile`、`TestScanReturnsHomePrefixedPath`，`tunnel: TestScanHostKeyFingerprintUnreachable`。其中原始输出包含 `mkdir /root/.ssh_scan_test_temp: mkdir /root/.ssh_scan_test_temp: read-only file system`。提升权限后重跑仍未通过，失败集合收敛为 `process: TestProbeReadyTimeout`、`sshkeys: TestScanSelectsOnlyPrivateKeys`、`TestScanSkipsUnreadableFile`、`tunnel: TestScanHostKeyFingerprintUnreachable`。
- Task 4 格式检查：`gofmt -l .` 实际仅输出既有文件 `api/agent_probe_test.go`；本计划改动的 10 个 Go 文件单独检查无输出，未越界修改该既有文件。
- Task 4 范围检查：相对 `e221ce74`（`git merge-base HEAD origin/feat/remote-integration-install-sdd`）的完整 diff 仅含本计划列出的 10 个 `agent/` 文件；未包含 README、desktop 或其它范围文件。
- 真机双机验收未执行，留给审核者按计划验收。
