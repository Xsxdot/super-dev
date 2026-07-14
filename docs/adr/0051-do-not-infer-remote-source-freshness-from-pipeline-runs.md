# Do not infer remote source freshness from pipeline runs

仅使用 Remote Workspace Preparation Pipeline 时，SuperDev 只能确定 Pipeline Run 的 pending、running、success 或 failed，不能从任意 Pipeline 成功推导远程代码必然 current，也不能在本地文件变化后无额外契约地判定 stale。因此首版不建立 desired/observed source revision 状态机，ADR-0048 由本决策取代。

## Consequences

Remote Workspace Replica 配置一个默认重启动作：执行 Preparation Pipeline 后重启，或直接重启。运行界面使用单个 split button 呈现默认动作，并允许用户在下拉菜单中对当次操作覆盖。Pipeline Run 状态保持为执行状态，不对外表达为代码已同步或已过期。
