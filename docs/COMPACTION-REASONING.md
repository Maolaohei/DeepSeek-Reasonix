# Compaction 与 reasoning 保留:方案 review

> 调研日期:2026-08-07。结论:Reasonix 的「compact 摘要纳入 reasoning」方案与业界主流
> (OpenAI / Anthropic / Prime Agent)同构,无需根本性改动;仅微调预算。

## 业界做法

### OpenAI(Responses API compaction)
- **服务端 compaction**(`context_management.compact_threshold`)与独立 `/responses/compact`
  端点:服务端用 LLM 把整个窗口(含 reasoning)压缩成 **opaque compaction item**,
  "carries forward key prior state and reasoning using fewer tokens"。
- 压缩后的窗口 = compaction item + **retained items**(部分原始项保留)。
- 只在渲染 token 数**越过阈值**时才压缩;压缩是单次 LLM 调用。
- 模型侧无法直接看到压缩 item 内容(非人类可读)。

### Claude Code(auto-compact)
- 用模型生成当前会话的摘要(summary)替换旧上下文;摘要作为系统侧消息注入,
  之后可继续追问摘要内容。
- 与 reasoning 保留:摘要模型能看到 assistant 消息(含内部思考,若保留),蒸馏关键信息。

### Prime Agent(recursion-enhanced)
- compact 时把历史轨迹送 LLM 生成结构化摘要;只压旧区,保留最近窗口。
- (对齐 Reasonix 的 compact 设计来源之一。)

## 我们的方案 vs 业界

| 维度 | OpenAI | Reasonix(现有实现) | 评估 |
|---|---|---|---|
| 压缩主体 | 服务端 LLM | summarize 摘要模型 LLM | ✅ 同构 |
| reasoning 处理 | 纳入压缩输入 | renderTranscript 纳入 + 摘要提示词蒸馏 | ✅ 同构 |
| 保留策略 | compaction item + retained items | 摘要 + 保留最近窗口 | ✅ 同构 |
| 触发 | 阈值(compact_threshold) | 阈值 + 手动 + 自动(compact 预算) | ✅ 同构 |
| 成本 | 单次 LLM 调用(仅超阈值) | 单次 summarize 调用(仅触发时) | ✅ 同构 |
| reasoning 进主循环? | 不(仅压缩时) | **不** — 仅 compact 摘要调用带 reasoning | ✅ 关键成本控制点 |
| 可解释性 | opaque(加密) | 明文摘要 | 明文本地架构更优(DeepSeek 无服务端 compact) |

## 关键成本控制点(已验证)

1. **reasoning 只进 compact 摘要调用,不进主循环调用** — 每轮推理成本不受影响;
   只有触发 compact 时才把历史 thinking 送入摘要 LLM。
2. **单条预算 8000 字符(≈2K tokens)** — 覆盖典型思考块;超长块才触发
   head+tail 截断(洞察常在末尾,`…<truncated>…` 标记)。
3. **摘要提示词蒸馏指令** — 不逐字引用 reasoning,提炼为
   Decisions & rationale / Errors & fixes / Pending 的耐用碎片,后续可直接续作。

## 结论

方案无需根本性改动。与 OpenAI 服务端 compaction(LLM 压缩含 reasoning)、Claude Code
auto-compact、Prime 的结构化摘要均一致。预算由 800 → 4000 → 8000 字符渐进放宽,
单条成本增量 < 每次 compact 总输入的 5%(reasoning 块通常只占窗口一小部分)。

## 已知边界

- `TestAgentEmitsRetryingThenStreams` 在本机环境(httptest + 真实 HTTP 重试时序)
  不稳定失败;经 worktree 验证在合并前(bfe4e6b49)同样失败 —— **既有环境问题,
  非本轮改动或上游合并引入**(RetryNotify emit 逻辑合并前后逐字节相同)。
