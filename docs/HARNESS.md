# Continual Harness — Reasonix 自主改进框架设计

> 状态:draft · 目标版本:下一里程碑
> 基准:ARC-AGI-3(评测方式见 §9)

## 1. 背景与动机

Prime Agent(PrimeIntellect-ai/prime-agent)的核心贡献是 **Continual Harness**(持续型 Harness):
把"外部框架"从静态设计改成**模型可自主精炼的持久状态层**。公开结果中,同样的模型
(Opus 5)在 ARC-AGI-3 上从 30.2% 冲到 95.5%,超过人类专家基线(95.4%)——分数差异
几乎全部来自 Harness 层,而不是模型本身。

它证明:当模型能力足够强时,固定工具接口、硬编码子代理、静态提示词这些"为上一代
模型设计"的框架约束会变成束缚;让模型通过小的、有证据支持的更新去改进自己外部
的持久状态,可以释放模型潜力。

Reasonix 已有成熟的 `memory`(remember/forget、revision/restore、scope)、`skill`
(技能发现/安装)和 `task`/`subagent`(profile)系统,但缺少 Prime 的**两个关键机制**:

1. **模型可编辑的补充提示词层(prompt notes)** —— REASONIX.md/AGENTS.md 是用户文件,
   模型只能 `#<note>` 追加一行,不能创建/更新/删除条目;
2. **`/refine` 精炼闭环** —— 独立 LLM 调用分析轨迹 → 产出结构化编辑 → 应用 +
   历史 + 回滚,以及 compaction 后的自动精炼 gate。

本设计把 Continual Harness 的理念吸收进 Reasonix,**映射到现有系统**(不重复造
memory/skill),保持 Cache-first 前缀字节稳定原则不变。

## 2. 设计原则

| Prime 原则 | Reasonix 落地 |
|---|---|
| 基础系统提示词不可变 | 现有 cache-stable 前缀(base prompt + 工具 schema + REASONIX.md/AGENTS.md + memory index)永不因 refine 改动 |
| 补充层可精炼 | 新增 harness 补充层(§4),模型可 create/update/delete |
| 小编辑 + 证据 | 每个编辑必须带 `reason`(轨迹证据);反对大段重写,鼓励最小 diff |
| 默认 local、显式 global | 默认写项目级(`projects/<slug>/harness`),`--global` 才写用户级(`harness/global`)——与 memory 的 project/global scope 语义一致 |
| 快照回滚 | 每次精炼记录 before/after + 事件到 `refinements.jsonl`;`/refine --rollback <id>` 逆序恢复 |
| 持久状态可发现 | harness 概览(条目列表)在精炼时作为上下文提供给模型 |

**Cache-first 不变量**:harness 编辑即时生效走 turn-tail(`<harness-update>`,与
`<memory-update>` 同机制,见 `internal/control/input.go` 的 `composeWithGoal`);
新条目在下一个 session 折叠进稳定前缀,此后每 turn 零成本。任何精炼都不允许
在 session 中途改写已缓存的系统前缀。

## 3. 四类 entry 与现有系统的映射

Prime 的 harness 有 `prompt | memory | skill | subagent` 四类。映射如下:

| kind | Reasonix 落点 | 现有机制 | 需要新增 |
|---|---|---|---|
| `prompt` | 新增 prompt notes 存储(§4) | boot 系统前缀注入点 | `internal/refine` 包的存储 + 加载 |
| `memory` | `memory.Store` | `remember`/`forget` 工具、`/memory restore` 回滚 | refine 侧桥接(编辑 → `Store.Save`/`Delete`) |
| `skill` | `skill.Store`(markdown SKILL.md) | `CreateWithContent`、技能重发现 | refine 侧桥接 + 重发现触发 |
| `subagent` | `skill.Store` 中 `runAs: subagent` 的 profile | `task`/`fleet`/`run_skill` 的 `ProfileLookup` | refine 侧桥接(编辑 → 写/删 profile SKILL.md) |

理由:memory 的 revision/scope/restore 已覆盖 Prime 的 memory 回滚需求;Reasonix 的
profile 技能就是"可复用子代理规格"的既有载体;skill 的 markdown 格式是 Reasonix
的技能契约。四类都归到已有系统,harness 只新增 `prompt` 这一个真正缺失的存储。

## 4. 存储与加载

### 4.1 目录布局(与 memory 同根)

```
<user-dir>                          # REASONIX_STATE_HOME / REASONIX_HOME / 默认状态根
├── projects/<workspace-slug>/
│   ├── memory/                     # 现有:项目级记忆
│   └── harness/                    # 新增:项目级(local)harness
│       ├── prompts/<slug>.md       # prompt notes(带 frontmatter)
│       ├── skills/<name>/SKILL.md  # refine 创建的技能(可选,通常走 skill.Store)
│       └── refinements.jsonl       # 精炼历史(事件日志)
└── harness/global/                 # 新增:用户级(global)harness
    ├── prompts/<slug>.md
    └── refinements.jsonl
```

`internal/refine` 新增 `StoreFor(userDir, cwd)` 镜像 `memory.StoreFor` 的目录解析。

### 4.2 prompt note 条目格式

```markdown
---
id: arc-grid-dedup-rule
title: Deduplicate rows before grid expansion
scope: project            # project | global
version: 3
updated: 2026-07-01T00:00:00Z
created: 2026-07-01T00:00:00Z
---

<补充行为策略,低优先级,排在 REASONIX.md/AGENTS.md 之后>
```

- slug 生成与 memory 一致(小写、连字符、80 字符截断);
- 内容即策略正文;加载时按 `updated` 升序拼接,标记 `<harness-prompt>` 块,
  注入系统前缀(在 memory doc 之后、memory index 之前,见 §4.3);
- 条目数上限(默认 32)与单条长度上限(默认 4KB),超出拒绝并提示先删除;
- 更新不丢历史:写前把旧内容追加到 `prompts/.history/<slug>.mdl`(或直接复用
  memory 的 revision 思路,存 `revision` 计数),`/refine --rollback` 时恢复。

### 4.3 加载点(boot)

`internal/boot/boot.go` 系统前缀组装处(memory 折叠进前缀的位置,~L581)之后追加
harness prompt 块。**只有 project + global 两级合并视图进前缀**;session 内新增的
条目走 turn-tail,不碰前缀。加载失败(损坏文件)降级为空,不阻塞启动(与
`loadHarnessState` 的 Prime 行为一致)。

## 5. `/refine` 精炼闭环

### 5.1 触发与暴露

- **slash 命令** `/refine [--global] [--rollback <id>] [<instructions>]` —— 用户手动触发;
- **模型工具** `refine`(带 `instructions`、`scope` 参数)—— 模型在轨迹中自主触发;
  工具执行需要权限审批(与 `remember`/`forget` 同级或更严:harness 编辑影响未来
  所有 session,默认走 approval)。

### 5.2 输入(一次独立 LLM 调用)

复用 `internal/boundedllm` 的独立无工具调用模式(Auto Guard / Goal evaluator 同款),
但输出预算放大(参考 Prime:`min(model.maxTokens, 32_000)`),**强制非推理模式**
(JSON 输出需要全部 final-text 预算;Prime 的注释明确记录了这一点)。

```
<current_harness_state>   概览:prompt notes 标题+摘要、memory index、skills、profiles
<refinement_history>      最近 N 次精炼事件(id、changes、outcome)
<conversation>            轨迹切片(最近 ~80K 字符,与 compaction 输入同源)
<scope_policy>            local/global 策略说明
<user_refine_instructions>  可选
→ 仅输出 JSON:{"summary","rationale","expectedOutcome","edits":[{action,kind,id,title,content,reason}]}
```

系统提示词约束(吸收 Prime 的 `REFINEMENT_SYSTEM_PROMPT`):
- base system prompt / REASONIX.md / AGENTS.md 不可编辑;
- 小编辑、有证据;无价值的编辑返回空 edits;
- `memory` 用于事实/偏好,`prompt` 用于行为策略,`skill` 用于可复用流程,
  `subagent` 用于可复用委托角色;每次只动最小相关组件;
- 默认 local;global 只用于跨 session 稳定经验或显式项目限定事实。

### 5.3 校验与冲突检测

应用前逐条校验(`validateEdit` 同款):
- action ∈ {create, update, delete};kind ∈ {prompt, memory, skill, subagent};
- create/update 必须带 title + content;delete 必须带 id;
- **冲突检测**:规划(LLM 调用)期间状态可能被并发写入——规划时快照 baseline,
  应用前重读文件,`before != baseline` 的编辑拒绝("entry changed during refinement
  planning"),同一次提案内已改过的 key 跳过该检查。

### 5.4 应用

- `prompt` → 写/删 prompt note 文件,`version+1`,queue turn-tail note
  (`<harness-update>`);
- `memory` → `memory.Store.Save`(update 时带 `ExpectedRevision`)/`Delete`;
- `skill` → `skill.Store.CreateWithContent`(带 `runAs: subagent` 即 subagent 类)
  或删除,并触发技能重发现;
- `subagent` → 同上(profile SKILL.md 即 subagent 规格)。

### 5.5 历史与回滚

- 每次精炼追加一条事件到 `refinements.jsonl`:`{id, trigger, changes[], evidence,
  outcome, before[], after[], created_at}`;
- `/refine --rollback <id>`:从事件还原逆编辑(有 before → update/create,无 before
  有 after → delete),作为一次新的精炼事件记录(`rollbackOf: <id>`);
- `refinements.jsonl` 有大小上限(如 1MB),滚动截断,但 before/after 快照保留在
  事件内直到被截断。

## 6. auto-refine gate(自动精炼)

触发点:**compaction 完成后**(Prime 在 turn 间隔与 compact 两个点,Reasonix 先做
compact 一个点,改动面最小)。

- 独立小 LLM 调用(输出预算 ~4K,非推理模式),输入 = 轨迹尾部 + harness 概览 +
  精炼历史,输出 JSON `{"shouldRefine": bool, "rationale", "instructions"}`;
- `shouldRefine=true` 才执行一次 local refine;
- 节流:距上次精炼至少 N 次 assistant turn(默认 8),防止频繁触发;
- 开关:`[harness] auto_refine = true|false`(默认 false,先手动验证再放开)。

## 7. 配置项(`reasonix.toml`)

```toml
[harness]
enabled = true            # 整体开关
auto_refine = false       # compact 后自动精炼 gate
max_prompt_notes = 32     # prompt notes 条目上限
max_prompt_note_bytes = 4096
refinements_log_bytes = 1048576
```

## 8. 安全边界

- **不可编辑**:base system prompt、REASONIX.md、AGENTS.md、用户自建 skill(非
  refine 创建)、memory doc 文件——refine 只写 harness 目录 + 经 Store API;
- 所有 harness 编辑走工具审批(默认),`/refine` 命令结果在执行前展示 edits 预览;
- 条目上限防膨胀;rollback 保证可逆;
- refine 的 LLM 调用计入独立 usage source(`event.UsageSourceRefine`),不污染主
  session 的 prompt cache 统计。

## 9. ARC-AGI-3 验证方案

评测形态(与 ARC 官方评测一致):一个 session 内连续呈现多个未见任务;agent 自主
长程运行;跨任务复用决定得分。

- **评测隔离**:每次评测运行使用独立 worktree + 独立状态根
  (`REASONIX_STATE_HOME` 指向临时目录),project 级 harness 天然隔离;
  global 级条目在评测前清空;
- **对照**:同一模型、同一提示词基线,开关 harness(空 harness vs 开启
  `/refine` + auto-refine)对比 ARC-AGI-3 得分;
- **过程指标**:精炼频率、编辑类型分布、rollback 次数、prompt notes 对后续任务
  命中率(是否真的被读到并影响行为);
- **目标**:复现"harness 开启后得分显著高于静态 harness"(参考:Prime 报告
  30.2% → 95.5%)。低于预期时重点检查:轨迹切片质量、概览是否过载、
  local/global 策略是否把经验写到了正确层级。

## 10. 实施步骤

| 阶段 | 内容 | 涉及包 |
|---|---|---|
| P1 核心闭环 | `internal/refine` 存储(prompt notes + refinements 日志)+ boot 加载注入 + `/refine` slash 命令 + prompt 类编辑 | `internal/refine`(新)、`internal/boot`、`internal/control`、`internal/command` |
| P2 工具与全类目 | `refine` 模型工具 + memory/skill/subagent 编辑桥接 + rollback + 审批 | `internal/tool/builtin`、`internal/control`、`internal/skill` |
| P3 自动 gate | compact 后 auto-refine gate + 节流 + 配置项 | `internal/agent`、`internal/config` |
| P4 评测验证 | ARC-AGI-3 评测脚本 + 对照实验 + 过程指标 | `benchmarks/`、`scripts/` |

每阶段独立可测:P1 后 `/refine` 能手动把策略写入 prompt notes 并跨任务生效;
P2 后模型能自主触发;P3 后无人值守可跑评测。

### 实施状态

- **P1 已完成**:`internal/refine` 包(prompt notes 存储、refinements.jsonl 事件
  日志与滚动、编辑校验/冲突检测/应用、rollback 逆编辑、概览/历史渲染、boundedllm
  的 Plan 与 review-gate 调用)、boot 系统前缀注入(`refine.Compose` 紧凑摘要)、
  `/refine [--global] [--rollback <id>] [instructions]` slash 命令(Submit + TUI +
  补全)、turn-tail `<harness-update>` 即时生效、`event.UsageSourceRefine`。
- **P2 已完成**:`refine` 模型工具(ReadOnly=true,免审批自主触发,与 Prime Agent
  一致);memory/skill/subagent 编辑桥接——memory 走 `memory.Store.Save/Archive`
  (自带 revision/restore 回滚),skill/subagent 走 `skill.Store.CreateWithContent/
  Delete`(subagent 规格自动补 `runAs: subagent` frontmatter,动态重发现)。
- **P3 已完成**:auto-refine gate 双触发点(与 Prime Agent 对齐)——每次自动
  compact 成功后 + **每 25 个完成的用户轮**(`auto_refine_interval_turns`,Prime
  的 turn_interval 默认值,0 可关)触发 `ReviewGate` 独立小 LLM 调用判断轨迹是否
  有可复用经验,通过则自动执行 project 级精炼(默认开启,10 分钟节流,两触发点
  共享节流);配置 `[harness] enabled / auto_refine /
  auto_refine_min_interval_minutes / auto_refine_interval_turns`(指针字段:旧配置
  无此段默认启用)。模型工具 + 双触发 auto-refine 使 harness 成为长任务的**自动
  机制**,短 session/任务边界频繁切换的场景也无需手动 `/refine`。
- 待办:P4 评测验证(ARC-AGI-3 对照实验,harness 已就绪)。

## 11. 参考

- Prime Agent:https://github.com/PrimeIntellect-ai/prime-agent(README、`docs/rlm.md`、
  `packages/coding-agent/src/core/refinement/refinement.ts`)
- Continual Harness 论文:https://arxiv.org/abs/2605.09998
- Reasonix 相关:REASONIX.md(Cache-first 原则)、`internal/memory`(scope/revision)、
  `internal/boundedllm`(独立 reviewer 调用)、`internal/control/input.go`(turn-tail)
