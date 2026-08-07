import { strict as assert } from "node:assert";
import { isPlainMarkdown } from "../components/Markdown";

// isPlainMarkdown must be conservative: everything the detector cannot prove
// to be markdown falls through to the full react-markdown pipeline, so false
// positives (markdown rendered literally) are the only hard failure mode.
function expect(actual: boolean, want: boolean, label: string) {
  assert.equal(actual, want, label);
}

// Plain prose must take the fast path.
expect(isPlainMarkdown("今天天气很好，我们继续写第五章。"), true, "chinese prose is plain");
expect(isPlainMarkdown("The chapter continues with the same pacing as before."), true, "english prose is plain");
expect(isPlainMarkdown("Hello, world!\nSecond line stays visible."), true, "line breaks are plain");
expect(isPlainMarkdown(""), true, "empty is plain");
expect(isPlainMarkdown("   "), true, "whitespace only is plain");

// Any markdown construct must fall through to the real renderer.
expect(isPlainMarkdown("## 第五章"), false, "heading");
expect(isPlainMarkdown("> 引文"), false, "blockquote");
expect(isPlainMarkdown("- 列表项"), false, "list dash");
expect(isPlainMarkdown("1. 第一点"), false, "ordered list");
expect(isPlainMarkdown("重点 **加粗** 内容"), false, "bold");
expect(isPlainMarkdown("~~删除线~~"), false, "strikethrough");
expect(isPlainMarkdown("`inline code`"), false, "inline code");
expect(isPlainMarkdown("```go\nfmt.Println()\n```"), false, "code fence");
expect(isPlainMarkdown("[链接](https://example.com)"), false, "link");
expect(isPlainMarkdown("![图](a.png)"), false, "image");
expect(isPlainMarkdown("| a | b |\n|---|---|"), false, "table");
expect(isPlainMarkdown("<div>html</div>"), false, "html block");
expect(isPlainMarkdown("---"), false, "hr");
expect(isPlainMarkdown("第一行\n- 第二行开始列表"), false, "list after newline");

// Ordinary punctuation that is not markdown syntax must not trip the detector.
expect(isPlainMarkdown("版本 v1.2.3 于 2026-08-07 发布。"), true, "dots in prose");
expect(isPlainMarkdown("50% 完成，剩下 20 项。"), true, "percent and digits");
expect(isPlainMarkdown("（注意：括号内说明）"), true, "parentheses");
expect(isPlainMarkdown("A - B = C"), true, "inline dash not a list");
expect(isPlainMarkdown("价格为 $3.5 元"), true, "dollar sign");

console.log("PASS markdown-plain-detection");
