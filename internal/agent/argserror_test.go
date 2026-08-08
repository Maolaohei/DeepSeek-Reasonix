package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// A model that emits structurally-invalid JSON for a tool's args should get the
// tool's schema echoed back, so the retry lands a valid shape instead of
// repeating the same broken one (the "invalid character ':' after array element"
// case from the ask tool).
func TestMalformedToolArgsEchoSchema(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(NewAskTool())
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("c1", "ask", `{"questions":["q":1]}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)
	if err := a.Run(context.Background(), "ask me"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := toolResult(a.session, "ask")
	if !strings.Contains(got, "not valid JSON") || !strings.Contains(got, `"options"`) {
		t.Fatalf("malformed-args result should echo the schema, got %q", got)
	}
}

// A valid-JSON arg that fails the tool's own validation must surface that error
// as-is, without the schema hint (the shape was fine; the values weren't).
func TestValidArgsErrorOmitsSchema(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(NewAskTool())
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("c1", "ask", `{"questions":[{"question":"q","header":"h","options":[{"label":"a"}]}]}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)
	if err := a.Run(context.Background(), "ask me"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := toolResult(a.session, "ask")
	if strings.Contains(got, "not valid JSON") {
		t.Fatalf("a valid-JSON arg error must not get the schema hint, got %q", got)
	}
	if !strings.Contains(got, "two options") {
		t.Fatalf("expected the real validation error, got %q", got)
	}
}

// A JSON-valid arg whose field type mismatches the tool's schema (the "json:
// cannot unmarshal" class) must also get the schema echoed: the model guessed
// the shape wrong, and a bare decoder error does not say what shape to use.
func TestFieldDecodeErrorEchoesSchema(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(NewAskTool())
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("c1", "ask", `{"questions":[{"question":"q","header":"h","options":"not-an-array"}]}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)
	if err := a.Run(context.Background(), "ask me"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := toolResult(a.session, "ask")
	if !strings.Contains(got, "not valid JSON") || !strings.Contains(got, `"options"`) {
		t.Fatalf("field-decode error should echo the schema, got %q", got)
	}
	if !strings.Contains(got, "cannot unmarshal") {
		t.Fatalf("expected the decoder error to remain visible, got %q", got)
	}
}
