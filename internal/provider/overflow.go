package provider

import (
	"regexp"
	"strings"
)

// overflowPatterns match provider error text that signals the request exceeded
// the model's context window. Adapted from the pattern table pi keeps for its
// multi-provider overflow recovery (pi packages/ai/src/utils/overflow.ts).
var overflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)prompt is too long`),                                       // Anthropic
	regexp.MustCompile(`(?i)maximum context length`),                                   // OpenAI-compatible (LiteLLM, Together)
	regexp.MustCompile(`(?i)context length exceeded`),                                  // generic
	regexp.MustCompile(`(?i)exceeds? (?:the )?(?:model'?s )?maximum context`),          // OpenRouter, GitHub Copilot
	regexp.MustCompile(`(?i)exceeds? (?:the )?(?:available )?context (?:window|size)`), // OpenAI, llama.cpp
	regexp.MustCompile(`(?i)input is too long for requested model`),                    // Amazon Bedrock
	regexp.MustCompile(`(?i)reduce the length of the messages`),                        // Groq
	regexp.MustCompile(`(?i)too many tokens`),                                          // generic
	regexp.MustCompile(`(?i)token limit exceeded`),                                     // generic
	regexp.MustCompile(`(?i)maximum prompt length`),                                    // xAI (Grok)
	regexp.MustCompile(`(?i)context (?:window|length) too small`),                      // Ollama-style small window
}

// nonOverflowPatterns exclude lookalike errors that must not be routed to
// compaction: transient throttling (pi keeps the same Bedrock exclusion) and
// request-size errors, whose "too many tokens" wording is not a window overflow.
var nonOverflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)rate limit`),
	regexp.MustCompile(`(?i)too many requests`),
	regexp.MustCompile(`(?i)throttl`),
	regexp.MustCompile(`(?i)request (?:entity|body) too large`),
	regexp.MustCompile(`(?i)content length`),
}

// IsContextOverflowError reports whether err means the request exceeded the
// model's context window, as opposed to a transient or caller error. The agent
// uses it to route overflow to compaction-and-retry instead of plain retry.
func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, ex := range nonOverflowPatterns {
		if ex.MatchString(msg) {
			return false
		}
	}
	for _, pat := range overflowPatterns {
		if pat.MatchString(msg) {
			return true
		}
	}
	return strings.Contains(msg, "context window exceeded")
}
