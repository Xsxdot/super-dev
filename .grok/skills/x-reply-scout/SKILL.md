---
name: x-reply-scout
description: 手动执行 SuperDev 的 X 回复选帖与起草：优先搜过去 24 小时内与「多服务日志、端口/进程、dev server 重启、服务管控/审批」相关的需求与抱怨帖（与产品楔子同频），按 launch reply playbook 起草 ≤280 字英文回复，写入项目 tmp/gtm-scout-YYYY-MM-DD.md。不发帖、不 commit、不定时任务。Use when the user runs /x-reply-scout, or says 每日选帖、X 回复草稿、搜抱怨帖、GTM scout、日志端口进程需求、reply scout、找可回的帖。
---

# x-reply-scout

手动 GTM 选帖 + 起草回复。账号 @gosuperdev。人审后再发，**绝不自动发帖**。

## 产品楔子（选帖准心，宁缺毋滥）

只保留和 SuperDev 论点**直接同频**的帖。高优先级痛点（按重要度）：

1. **日志** — agent 读不到/拼不对多服务日志；paste logs；tee + AGENTS.md；「logs tell where not why」
2. **端口 / 进程** — EADDRINUSE、zombie dev server、端口被占、不知道谁占了 port、杀不干净
3. **服务生命周期** — 假重启、agent 说 restart 了其实没、background process 失控、codex/claude 管不了 dev server
4. **服务管控 / 同意（审批）** — 本地 dev 免打扰 vs 远端/生产要 preview→approve；「agent 乱重启」的恐惧与解法
5. **可观测调试闭环** — attach 断点、browser 验证、claims it fixed 但没验证

**降权 / 通常跳过**：泛 AI 编程鸡汤、模型比拼、纯 IDE 主题、无运行时痛点的 vibe coding、已是竞品硬广、带链接的资讯转发。

论点金句（回复中段可用，勿复读同一条）：
- logs tell you where, state tells you why
- brilliant but blindfolded
- the agent is a clipboard until it owns the runtime
- restart is not a side effect you hope for — it's a controlled operation

## 何时跑

用户说 `/x-reply-scout` 或「跑今天的选帖」。默认窗口：**过去 24 小时**。若用户指定日期/更长窗口，听用户的。

## 步骤

### 1. 准备

- 项目根：当前 workspace（super-debug）
- 读一遍 `launch/2026-07-x-reply-playbook.md` 的分类与禁区（A/B/C、≤280、冷启动不塞链接）
- 关键词细节见 `references/keywords.md`（可随实战增删）
- `mkdir -p tmp`
- 日期用 **America/Los_Angeles**：`tmp/gtm-scout-YYYY-MM-DD.md`

### 2. 搜索（X 为主）

用 X keyword / semantic 搜索。每条查询加时间约束：`since:YYYY-MM-DD`（昨天 PT 日期）或工具的时间过滤；**丢掉 >24h 的结果**。

优先跑「日志 / 端口 / 进程 / 重启 / 服务管控」批次（见 keywords.md 的 **Wedge 批次**）。  
资讯帖用 `-filter:links`；可加 `min_faves:2` 滤完全零互动，但**小同行 A 类**可放宽。

可选：Reddit（r/ClaudeAI、r/ClaudeCode、r/cursor、r/mcp）只扫同楔子词，时间不够就跳过。

### 3. 筛选与分类

| 类型 | 特征 | 自报家门 | 链接 |
|------|------|----------|------|
| **A** 小同行 | 低互动、同战壕 | 可轻描 + 边界诚实 | 不放 |
| **B** 论点同频 | 中小帖、楔子重合 | 结尾一句轻度 | 不放 |
| **C** 大 V 热帖 | 高互动 | **零自报**，纯观点 | 绝不放 |

- 每天目标 **3–8 条** 可回候选；楔子不准的坚决丢，不要凑数。
- 去重：同作者同主题留 1 条。
- C 类先想清角度是否已被回复区占满。

### 4. 起草回复

- **英文**、**≤280 字符**、优先单段、无硬换行堆砌
- 配方：开头（资格 / 纠正 / buried lede）→ 中段可带走观点 → 贴回对方场景
- 禁止：so true、求转发、hashtag、冷启动塞产品链接/产品名（对方明确在找工具时例外）
- **每条措辞必须不同**

### 5. 落盘

写入 `tmp/gtm-scout-YYYY-MM-DD.md`：

```markdown
# GTM Daily Scout — YYYY-MM-DD (America/Los_Angeles)

## Meta
- Run at: <ISO, PT>
- Window: last 24h
- Focus: logs / ports / process / service control
- Candidates kept: N | Skipped: N

## Candidates (ranked)

### 1. [A|B|C] @handle — one-line hook
- **URL**: ...
- **Posted**: ...
- **Wedge**: logs | ports | process | restart | approval | browser-verify | ...
- **Why**: 1–2 句
- **Angle**: agree-and-sharpen | correct | answer-question | ...
- **Draft reply** (paste-ready, ≤280 chars):

  <english reply>

- **Risk notes**: ...

## Skipped (top reasons)
- ...

## Follow-ups
- 今日优先回的 Top 3（按 ROI）
```

### 6. 向用户汇报

只报：文件路径、保留条数、Top 3 一句话摘要。  
**不** git add/commit，**不**发帖，**不**装 launchd。

## 失败降级

- 某批关键词 0 结果 → 继续下一批，不整任务失败
- X 工具不可用 → 说明限制，给出用户可粘贴的搜索串列表，仍尽量写空骨架文件

## 反例（不要选）

- 「Claude vs GPT 哪个写代码好」
- 无运行时场景的 prompt 技巧帖
- 纯招聘 / 课程广告
- 论点是「agent 可观测性平台（tracing LLM calls）」但**不是**本地 dev runtime 日志/端口——相邻不相同，易写成错位回复
