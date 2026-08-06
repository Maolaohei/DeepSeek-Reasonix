# RLM 设计吸收评估 — Reasonix

> 状态:评估完成 · 结论:核心理念大多已通过现有机制对齐,不建议照搬 IPython
> 架构;差异点(持久计算状态、子代理双向消息)以最小侵入方式落地。

## 1. Prime Agent 的 RLM 是什么

Recursive Language Model(RLM)是 Prime Agent 的核心执行模型:

- **持久 IPython kernel 是模型唯一的内置工具**:读文件、shell、数据转换、技能调用、
  递归委托全部从一段 Python 代码开始;变量/函数/结果跨 turn 存活,模型有真正的
  "工作记忆"。
- **递归子代理是原生调用**:`await rlm("任务", name="x")` 立即返回句柄(不阻塞),
  子代理独立 session 运行,结果只通过显式 `agent_message` 回复或文件返回;父可
  用句柄继续向子发消息。
- **prompt-as-a-variable**:系统提示词由 host 从基础提示词 + harness 状态组装,
  模型无法改写基础提示词。
- **宿主 bridge**:`goal.*`、`agent_message.*`、`rlm_heartbeat`、`compact` 等通过
  `host.request` 类型化调用,凭据/执行/持久化始终留在宿主。

## 2. 与 Reasonix 的映射(现状)

| RLM 要素 | Prime 实现 | Reasonix 对应 | 状态 |
|---|---|---|---|
| 递归子代理 | `rlm()` 原生调用、深度可配 | `task`/`fleet`/`parallel_tasks` + 递归深度配置 + profile | ✅ 已对齐 |
| 子代理可复用规格 | harness `subagent` 条目 | subagent 类编辑 → `runAs: subagent` profile 技能(P2) | ✅ 已对齐 |
| prompt-as-a-variable | host 组装系统提示词 | `boot` 组装 cache-stable 前缀 + Continual Harness 块 | ✅ 已对齐 |
| 宿主类型化请求 | `host.request` comm | 工具即宿主调用(goal 工具、refine 工具、jobs) | ✅ 已对齐 |
| 持久目标/后台/心跳 | goal/heartbeat/schedule/autonomous | `/goal`、后台 jobs、任务监控 | ✅ 已对齐 |
| 可执行技能 | Python 包(import 调用) | SKILL.md + 可选 shell 命令;程序化程度较低 | ⚠️ 部分 |
| 持久计算状态 | IPython 变量跨 turn | 无 REPL;模型靠文件 + harness/memory 持久 | ⚠️ 差距 |
| 子代理双向消息 | `agent_message`(父↔子) | 子代理结果返回 + 后台 jobs 消息;无中途定向消息 | ⚠️ 差距 |
| 一切皆程序化 | 一个 `ipython` 工具取代十几个工具 | 丰富内置工具集(设计哲学不同) | ❌ 不适用 |

## 3. 差距分析与建议

### 3.1 持久计算状态(RLM 的"工作记忆")

Prime 的 Python 变量跨 turn 存活,让模型免于重复解析/重算。Reasonix 无 REPL,
但已有等价载体:

- **harness prompt notes + memory**:策略与事实的持久状态(P2/P3 已落地);
- **工作区文件**:模型可用 `write_file` 维护自己的分析产物(评测中它确实这么做了:
  写 `vis.py` 可视化脚本)。

**建议(最小落地)**:在评测/长任务场景,已有路径够用。若要更接近 RLM,可加一个
轻量 `workspace-note` 工具(模型维护一份跨 turn 的工作笔记,自动进入 turn-tail
上下文),但优先级低——文件方案已覆盖 90% 需求。

### 3.2 子代理双向消息(agent_message 等价)

Prime 的父↔子消息让父不必轮询、子不必一次性返回。Reasonix 的 `task` 是阻塞等待
最终答案,`fleet`/后台任务通过结果引用取回——**中途定向通信**缺失。

**建议**:评估 `jobs` 系统是否可作为子代理消息通道(后台任务已有事件/结果机制)。
若可,加一个 `agent_message` 工具让父向后台子代理发消息;否则保持现状
(文件 + 结果引用已覆盖常见场景)。

### 3.3 可执行技能

Prime 技能是可 import 的 Python 包(带 typed API);Reasonix 技能是 markdown +
可选 shell。Reasonix 的 `/install` 与技能重发现机制已支持"带命令的技能"。

**建议**:不引入 Python 运行时;在 SKILL.md 契约中强化"命令化技能"文档即可。

### 3.4 不适用项

"IPython 作为唯一模型工具"与 Reasonix 的 Go + 丰富工具集架构互斥;吸收它会
丢弃现有工具生态(权限、审批、沙箱、缓存前缀设计),得不偿失。RLM 的价值主张
(持久状态、递归委托、宿主控制)已由对应机制覆盖。

## 4. 结论

RLM 的核心理念(递归子代理、prompt-as-a-variable、宿主 bridge、可复用子代理规格)
在 Reasonix 中**已通过既有机制对齐**;本次 Continual Harness(P1-P3)补上了
"模型可编辑的持久状态层"这一 RLM 的支撑部分。剩余差距(3.1/3.2)是可选的增量,
不阻塞"不需要 /refine 也能自动采用"的目标。
