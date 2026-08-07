package novel

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
	"unicode/utf8"
)

// scaffoldTemplates are the initial continuity assets of a new novel. They
// fold in the best of the surveyed tools: Sodarie's memory bank, Openwrite's
// outline hierarchy and craft rules, Maliang's settings tree and golden-three
// chapter method.
var scaffold = map[string]string{
	"brief.md": `# 小说定位（brief）

- 一句话 premise：
- 目标读者 / 阅读回报：
- 频道与发布形态：
- 故事发动机（主角的循环：欲望 → 阻力 → 选择 → 代价）：
- 确认记录（用户确认 / 用户授权 AI 决定 / AI 推荐待确认）：
`,
	"bible.md": `# 世界圣经（bible）

## 作品信息
- 题材（异世界转生 / 校园 / 日常 / 恋爱喜剧 / 战斗 / 悬疑…）：
- 世界观关键词（魔法 / 科技 / 特殊能力 / 系统 / 现代日本…）：
- 特殊机制（转生前提 / 系统面板 / 技能树 / 等级…）：
- 主要舞台（学校 / 异世界 / 社团 / 城市…）：

## 世界规则（这个世界什么能发生、什么不能）
- 

## 写作规则（你希望的笔法）
- 

## 禁止模式（不要出现的偷懒写法）
- 

## 叙事默认（POV / 视角角色 / 形式指令）
- 视角：第一人称"我" / 第三人称限制 / 客观旁观 / 全知
- 视角角色：
- 形式指令：
`,
	"characters.md": `# 角色档案（characters）

| name | 属性标签 | current_status | knows（已知道） | secrets（藏着） | relationships | constraints |
| --- | --- | --- | --- | --- | --- | --- |
|  |  |  |  |  |  |  |

> knows / secrets 是防剧透的关键：反派的真实身份只写进某人的 secrets，其他角色就不会提前点破。
> 关系可标注类型与阶段：父母 / 同学 / 伙伴 / 敌对 / 暧昧（好感度：陌生 → 认识 → 朋友 → 暧昧 → 恋人）。

## 角色设定（轻小说设定集风格）
- name：
  - 年龄 / 生日 / 身高：
  - 属性标签（傲娇 / 天然呆 / 冷静 / 腹黑 / 元气 / 病娇…）：
  - 口头禅 / 口癖：
  - 称呼习惯（叫别人什么 / 被叫什么）：
  - 角色定位（主角 / 女主 / 对手 / 吉祥物 / 吐槽役…）：
`,
	"outline.md": `# 全书大纲（outline）

## 总纲（premise + 结局方向）
- 

## 卷（文库一卷 = 序章 + 12-20 话 + 章末）
- 卷1：` + "`起`" + `（话数范围、阶段回报：女主登场 / 能力觉醒 / 第一次危机…）
- 卷2：
- 卷3：

## 话纲（最小写作单元）
- 第X话标题（标题可长句式）| 弧线位置（起/承/转/合/过渡）| 内容焦点（这几千字写什么）| 目标 | 节拍（beats）| 情绪变化 | 章末钩子（必须留悬念）
`,
	"foreshadowing.md": `# 伏笔账本（foreshadowing）

## active（埋下未回收）
- id：fs001 | 描述： | planned_resolution： | 回收窗口： | dramatize（true/false）：

## resolved（已回收）
- 
`,

	"style.md": `# 风格库（style）

**模仿笔风时，优先放原文片段**：直接照抄目标作者的原文（不改写、不概括、不"仿写一段给你看"），模型以原文为唯一权威模仿其调性。AI 自写样文只在你没有明确模仿对象时作兜底，且不得替代原文。

放 3-5 段原文片段，贵精不贵多。若混合两种笔风，分节标注（如"笔风 A：短句冷峻""笔风 B：絮叨吐槽"）。

## 原文片段 1（优先：作者原文，逐字照抄）
> 

## 写作偏好（从原文归纳，而不是先写偏好再找原文）
- 句长：
- 转场：
- 叙述距离：
- 人物对话习惯：
- 独白 / 吐槽风格（轻小说：主角内心活动承担喜剧与信息双重功能）：
`,
}

// Init creates a new novel workspace under <dir>/novels/<title> with the full
// scaffold and a state file pointing at the first step.
func Init(dir, title string) (string, error) {
	if err := validateTitle(title); err != nil {
		return "", err
	}
	ws := filepath.Join(dir, "novels", title)
	if _, err := os.Stat(ws); err == nil {
		return "", fmt.Errorf("workspace %s already exists", ws)
	}
	if err := os.MkdirAll(filepath.Join(ws, "chapters"), 0o755); err != nil {
		return "", err
	}
	for name, tmpl := range scaffold {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(tmpl), 0o644); err != nil {
			return "", err
		}
	}
	s := &State{
		Title:      title,
		CreatedAt:  time.Now(),
		NextAction: "fill brief.md, bible.md, characters.md, outline.md, then confirm opening settings (POV / length / boundaries) before writing ch001",
		Chapters:   map[string]*Chapter{},
	}
	if err := Save(ws, s); err != nil {
		return "", err
	}
	return ws, nil
}

// Status renders a human-readable progress line from the state: where the book
// stands and the single next step. The model and the user consume the same
// line, so continuation never depends on memory of the conversation.
func Status(ws string, s *State) string {
	var b []string
	b = append(b, fmt.Sprintf("作品：%s", s.Title))
	if s.LastStableChapter != "" {
		b = append(b, fmt.Sprintf("最后稳定章节：%s", s.LastStableChapter))
	} else {
		b = append(b, "尚未有稳定章节")
	}
	if len(s.Chapters) > 0 {
		total, stableN := 0, 0
		var rows []string
		for id, ch := range s.Chapters {
			if _, ok := chapterNum(id); !ok {
				continue
			}
			body, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(ch.Path)))
			runes := 0
			if err == nil {
				runes = utf8.RuneCount(body)
			}
			total += runes
			if ch.Status == "stable" {
				stableN++
			}
			sum := "✗"
			if _, err := os.Stat(filepath.Join(ws, filepath.Dir(filepath.FromSlash(ch.Path)), "summary.md")); err == nil {
				sum = "✓"
			}
			rows = append(rows, fmt.Sprintf("%s %s %d字 摘要%s", id, ch.Status, runes, sum))
		}
		sort.Strings(rows)
		b = append(b, "章节：", join(rows))
		b = append(b, fmt.Sprintf("进度：%d 章稳定 / %d 章登记 · 累计 %d 字", stableN, len(rows), total))
		var stale []string
		for id, ch := range s.Chapters {
			if ch.Status == "stale" {
				stale = append(stale, id)
			}
		}
		sort.Strings(stale)
		if len(stale) > 0 {
			b = append(b, fmt.Sprintf("⚠️ 待重新核对的章节（正文已变）：%s", join(stale)))
		}
	}
	if s.NextAction != "" {
		b = append(b, "下一步："+s.NextAction)
	}
	return join(b)
}

func join(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "\n"
		}
		out += p
	}
	return out
}
