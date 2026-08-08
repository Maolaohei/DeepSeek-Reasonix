package provider

import (
	"errors"
	"testing"
)

func TestIsContextOverflowErrorMatchesProviderTexts(t *testing.T) {
	cases := []string{
		"prompt is too long",
		"This model's maximum context length is 65536 tokens.",
		"input token count (123456) exceeds the maximum context length",
		"Request too large for model: exceeds the context window",
		"Error: input is too long for requested model",
		"Please reduce the length of the messages",
		"400: too many tokens, the request exceeds the model's context",
		"token limit exceeded",
		"maximum prompt length is 131072",
	}
	for _, msg := range cases {
		if !IsContextOverflowError(errors.New(msg)) {
			t.Errorf("expected overflow for %q", msg)
		}
	}
}

func TestIsContextOverflowErrorRejectsNonOverflow(t *testing.T) {
	cases := []string{
		"rate limit exceeded, retry later",
		"429 Too Many Requests",
		"ThrottlingException: too many tokens, please wait",
		"413 Request Entity Too Large: too many tokens in body",
		"request body too large (content length exceeds limit)",
		"connection reset by peer",
		"invalid api key",
		"model not found",
	}
	for _, msg := range cases {
		if IsContextOverflowError(errors.New(msg)) {
			t.Errorf("expected non-overflow for %q", msg)
		}
	}
}

func TestIsContextOverflowErrorHandlesWrappedErrors(t *testing.T) {
	err := errors.New("upstream")
	wrapped := errors.Join(err, errors.New("request exceeds the context window"))
	if !IsContextOverflowError(wrapped) {
		t.Error("wrapped overflow error not detected")
	}
	if IsContextOverflowError(nil) {
		t.Error("nil error must not be overflow")
	}
}
