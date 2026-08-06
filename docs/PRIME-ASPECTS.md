# Prime Agent 四维度对照 — Reasonix 强化点清单

> 状态:分析完成 · 对照 Prime Agent 的四大能力维度,逐条核对 Reasonix 现状,
> 标注差距与落地建议。已在本次优化中落地 5 项(见文末)。

## 维度 1:状态持久化与长程任务能力

| 能力 | Prime Agent | Reasonix 现状 | 差距 |
|---|---|---|---|
| Prompt-as-a-Variable / REPL | IPython kernel,文件路径=动态变量 | Compose 组装系统前缀;文件由工具读取 | ⚠️ 无 REPL(RLM.md 已评估:不适用,工具+harness 覆盖 90%) |
| Daemon 后台守护 | daemon 进程,终端断开继续 | serve(HTTP/SSE)、后台 jobs、recovery 分支、desktop 常驻 | ✓ 已覆盖 |
| 断线 attach 接续 | attach 恢复会话 | `--resume` + recovery branch(events.jsonl 权威日志) | ✓ 已覆盖 |
| 自动压缩 | compaction 保前缀 | `maybeCompact`(80% 阈值,缓存前缀保持) | ✓ 已覆盖 |
| 增量 Harness | /refine + 双触发 auto-refine | Continual Harness(P1-P3):refine 工具 + compact/25 轮双触发 + GuidanceBlock | ✓ 已覆盖(本次对齐) |
| **任务级 checkpoint** | 长任务阶段状态持久 | 会话消息 + goal-state + jobs | ⚠️ 无独立"任务进度 checkpoint"(todo/goal 有,跨会话恢复靠消息) |

**强化建议(低优先)**:长任务跨 session 恢复目前依赖会话消息重放;goal-state.json
已持久化 todo/goal,够用。无需新增。

## 维度 2:真实反馈与自我修正闭环

| 能力 | Prime Agent | Reasonix 现状 | 差距 |
|---|---|---|---|
| 沙盒执行环境 | IPython Kernel / Docker 沙盒 | bash 工具 + `internal/sandbox` + 审批门 | ✓ 已覆盖 |
| 测试驱动自修正 | pytest/go test 循环:报错→定位→补丁→重验 | 模型自主跑 go test + 工具;`evidence`/`delivery` 追踪成功工具调用 | ⚠️ 无显式"验证失败自动重试"机制(靠模型自觉) |
| Pass@1 反馈闭环 | 验证脚本驱动 | 模型在 prompt 引导下自行验证 | ⚠️ 引导弱,长任务中模型可能跳过验证 |

**强化建议(中优先)**:在系统提示词或 delivery 模式中强化"变更后必须运行验证并报告结果"的
契约。Reasonix 已有 evidence 基础设施,缺的是 prompt 层契约强度。

## 维度 3:编排机制与子代理协作

| 能力 | Prime Agent | Reasonix 现状 | 差距 |
|---|---|---|---|
| 程序化子代理 | `rlm("task")` 原生调用 | `task`/`fleet`/`parallel_tasks` + 递归深度 + profile | ✓ 已覆盖 |
| 并行分治 | 子代理独立上下文并发 | fleet(2-64 并行)+ 稳定结果引用 | ✓ 已覆盖 |
| 子代理结果干净汇总 | agent_message 显式传递 | 子代理最终答案 + read_subagent_result | ✓ 覆盖(拉取式) |
| **Agent-to-Agent 直接通信** | agent_message(父↔子、子↔子) | 无;子代理只能通过文件/结果引用间接通信 | ✗ **最大差距** |

**强化建议(高优先,工作量大)**:`agent_message` 通道(父向后台子代理发消息、子代理
间协作)是唯一结构性缺失。实现需要 jobs 系统扩展消息通道 + 工具暴露。**不建议本轮
实施**(风险高);文件 + 结果引用已覆盖多数场景。

## 维度 4:开发者体验与信任

| 能力 | Prime Agent | Reasonix 现状 | 差距 |
|---|---|---|---|
| 透明变动管理 | git 结合、清晰 commit/diff | git 工具 + diff/review/security-review + rollback | ✓ 已覆盖 |
| 渐进式自主权 | 预算(Turn/Token/Time)配置 | max_steps(turn 预算)+ context/output token 预算 + plan 模式 + 审批模式 | ✓ 已覆盖 |
| 可扩展技能库 | Python 包技能 | SKILL.md + install_source + 项目/全局 scope | ✓ 已覆盖 |
| 回滚机制 | refine rollback | `/refine --rollback` + `/memory restore` + git | ✓ 已覆盖 |

## 本次已落地的强化(性能/稳定性轮)

1. **read_file 大文件大小提示**(维度 2 工具链):首读 >64KB 文件即提示规模与翻页,
   直接修复大文件盲翻导致的 token 爆炸(败犬女主崩溃诱因)。
2. **auto-refine ReviewGate 窗口 80K→40K**(维度 1 成本):审查判断减半输入。
3. **refine 概览 memory index 上限 50**(维度 1 稳定性):大 memory 不膨胀精炼 prompt。
4. **refine Plan MaxTokens 16K→8K**(维度 1 速度):turn 内 refine 调用更快。
5. **auto-refine 失败静默**(维度 4 体验):后台自动精炼失败只写日志,不打断用户。

## 结论

四维度中三维度已完全对齐;剩余差距按优先度:
- **Agent-to-Agent 消息**(✗,高优先,需专项开发)
- **验证闭环契约强化**(⚠️,中优先,小改动)
- 任务级 checkpoint(⚠️,低优先,现状够用)

## 补充评估(基于后续审查)

### 验证闭环:delivery 模式已完整,普通模式建议"极弱提示"

审查 `finalReadinessCheckFor` 发现:**delivery 模式已实现用户建议的完整闭环**——
evidence 追踪成功 mutation/verification/review/signoff,模型准备 finish 时检查,
缺失则注入 missing 列表并通过 loopGuardAllowsFinal 控制放行。高强度场景已覆盖。

**普通模式**确实无验证 gate(设计取舍:不强制 delivery 仪式),但存在"模型改文件后
直接宣布完成"的风险。低成本方案评估:

- 注入点:turn 收尾时若 evidence 显示"有成功 mutation 但无成功 verification
  command",注入一条**软提示**(非阻止):"改动已写入;若存在对应验证(如 go test/
  pytest),请运行并报告结果"。
- 关键约束:**必须软提示、默认弱**——普通模式下"写文件≠需要验证"(文档/配置修改
  无测试可跑),强制 gate 会产生噪音、破坏轻量体验。
- 结论:可做(约 20 行),但优先级低于 Agent-to-Agent;建议与 delivery 复用同一
  evidence 基础,避免引入新模式。

### Agent-to-Agent 通信:认可两阶段路线

用户建议的 **Phase 1 拉取模式**(`poll_subagent_status(job_id)` +
`append_subagent_notes(job_id, note)` 公共留言板,基于 jobs 共享存储)评估:

- 低风险:不引入消息总线/WebSocket,复用现有 jobs 持久化;父子 agent 以文件式
  消息交互,天然可审计、可回滚。
- 与现有 `read_subagent_result`/fleet 引用互补(拉取式升级,非替代)。
- Phase 2(Mailbox Pattern)等异步底座稳定后再评估——同意。
- 结论:Phase 1 是合理的下一里程碑;本轮不实施(保持稳定性优先)。

### 实施级评估与决策(基于 evidence 基础设施复核)

复核 `internal/evidence/evidence.go` 确认关键 API 齐备:
`HasSuccessfulMutationOtherThan("remember")`(有成功变更)、
`HasSuccessfulVerificationCommand()`(有分类为 verification 的 bash 命令)、
`LatestSuccessfulMutationIndex()`(最新变更位置)。

**决策 1:普通模式验证软提示 —— 建议实施(下一小版)**

- 实现:复用上述 evidence API,在 turn 收尾组装(composeWithGoal 层)检查
  `hasMutation && !hasVerification && !deliveryProfile` → 注入一段**软提示**:
  "改动已写入;若存在对应验证(如 go test/pytest)请运行并报告结果,没有则简要说明。"
- 注入位置选 **composeWithGoal 文本层**(不碰 finalReadinessCheck/ReadinessResult),
  避免普通模式 UI 出现"未就绪"误导;只注入一次(模型准备结束且有写入无验证时),
  不每轮刷。
- 成本:约 40-60 行 + 测试。风险:低(措辞含"若存在…没有则说明",文档/配置类
  写入模型可合理跳过);不阻止(普通模式保持轻量)。
- 收益:普通模式长任务"改完直接宣布完成"的假完成率下降。

**决策 2:阻止 finish —— 仅限 delivery(现状已满足,普通模式不做)**

delivery 的 finalReadinessCheckFor + loopGuardAllowsFinal 已实现"无验证阻止/引导";
普通模式强制 gate 会产生噪音(并非所有写入都需要测试),维持现状。

**决策 3:A2A Phase 1 —— 建议独立小版本,非本轮**

复核发现 Phase 1 并非"轻量":需要 jobs 共享存储扩展(notes 读写)+ 两个新工具
(含 schema/审批/安全审查)+ **子代理侧支持**(启动时读取父代理 notes 并响应)。
估算 0.5-1 天工作量,风险中等(notes 注入子代理 prompt 需防提示注入)。当前
"文件 + read_subagent_result"已覆盖多数场景,不阻塞主线。

**优先级排序**:验证闭环软提示(低成本高收益)→ A2A Phase 1(独立版本)→ 任务级
checkpoint(低优先)。
