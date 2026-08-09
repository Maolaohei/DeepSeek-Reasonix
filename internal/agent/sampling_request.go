package agent

import (
	"context"
	"encoding/json"

	"reasonix/internal/provider"
)

// samplingRequest is a once-prepared, frozen provider request for one model
// round. All stream retries replay this exact payload — no synthetic recovery
// messages, no schema reorder, no previous_response_id drift from failed attempts.
type samplingRequest struct {
	req provider.Request
}

// maxTokensSafetyTokens keeps output inside the window with headroom; a
// near-full context must not push the request over the provider limit.
const maxTokensSafetyTokens = 4096

// minAnswerTokens is the floor below which clamping gives up and leaves the
// output budget untouched: with no room to answer, provider-side truncation
// and the truncated-tool-call guard handle the turn instead.
const minAnswerTokens = 1024

// clampMaxTokensToContext caps MaxTokens at the window minus the estimated
// input and a safety margin. A zero MaxTokens (inherit provider default) is
// left untouched: clamping it to the headroom could amplify a small inherited
// budget beyond the provider's output cap. A saturated window (< minAnswerTokens
// headroom) is also left alone.
func clampMaxTokensToContext(maxTokens, contextWindow, inputTokens int) int {
	if maxTokens <= 0 || contextWindow <= 0 || inputTokens >= contextWindow-maxTokensSafetyTokens-minAnswerTokens {
		return maxTokens
	}
	avail := contextWindow - inputTokens - maxTokensSafetyTokens
	if avail >= maxTokens {
		return maxTokens
	}
	return avail
}

// prepareSamplingRequest freezes one model-round request (preflight + interceptors).
func (a *Agent) prepareSamplingRequest(ctx context.Context) (samplingRequest, error) {
	// CreatedAt is durable UI metadata, not model input. Strip it from the
	// transport copy so wall-clock differences never invalidate the provider's
	// prompt-cache prefix (and custom providers cannot accidentally send it).
	if err := a.contextPreflight(ctx, CompactionTriggerPressure); err != nil {
		return samplingRequest{}, err
	}
	requestMessages := append([]provider.Message(nil), provider.ModelMessages(a.modelVisibleMessages())...)
	requestMessages = a.providerProjectionMessages(requestMessages)
	for i := range requestMessages {
		requestMessages[i].CreatedAt = 0
	}
	// context.prepare: extensions may rewrite the message copy feeding THIS
	// request. The session log is never touched — the replacement is
	// ephemeral, so the next request starts from the unmodified history and
	requestMessages, err := a.interceptContextPrepare(ctx, requestMessages)
	if err != nil {
		return samplingRequest{}, err
	}
	req := provider.Request{
		Messages:       requestMessages,
		Tools:          a.tools.Schemas(),
		MaxTokens:      a.maxOutputTokens,
		Temperature:    provider.OptionalTemperature(a.temperature),
		ResponseFormat: responseFormatFromRequest(ctx),
		EffortOverride: a.governorOverride(),
	}
	// provider.request: the fully assembled request gets one last ruling
	// (revalidated by the payload registry) before it goes on the wire.
	req, err = a.interceptProviderRequest(ctx, req)
	if err != nil {
		return samplingRequest{}, err
	}
	// Cap the output budget to the window headroom so a near-full context
	// cannot push the request over the provider limit (or truncate mid-tool).
	if a.contextWindow > 0 {
		req.MaxTokens = clampMaxTokensToContext(req.MaxTokens, a.contextWindow, estimateSamplingRequestInputTokens(req))
	}
	return samplingRequest{req: freezeProviderRequest(req)}, nil
}

// providerProjectionMessages applies provider-specific role compatibility to a
// request copy. Projection sidecars retain logical user-turn boundaries so
// explicit range compression can continue to resolve anchors across calls.
func (a *Agent) providerProjectionMessages(msgs []provider.Message) []provider.Message {
	if a != nil && a.strictAlternatingRoles {
		return coalesceProjectionUserRuns(msgs)
	}
	return msgs
}

// freezeProviderRequest deep-copies the provider-visible request surface so
// retries share identical messages, tools order, temperature, and format.
func freezeProviderRequest(req provider.Request) provider.Request {
	out := req
	if len(req.Messages) > 0 {
		out.Messages = append([]provider.Message(nil), req.Messages...)
		for i := range out.Messages {
			if len(out.Messages[i].ToolCalls) > 0 {
				out.Messages[i].ToolCalls = append([]provider.ToolCall(nil), out.Messages[i].ToolCalls...)
			}
			if len(out.Messages[i].Images) > 0 {
				out.Messages[i].Images = append([]string(nil), out.Messages[i].Images...)
			}
			if len(out.Messages[i].ResponsesItems) > 0 {
				items := make([]json.RawMessage, len(out.Messages[i].ResponsesItems))
				for j, item := range out.Messages[i].ResponsesItems {
					items[j] = append(json.RawMessage(nil), item...)
				}
				out.Messages[i].ResponsesItems = items
			}
		}
	}
	if len(req.Tools) > 0 {
		out.Tools = make([]provider.ToolSchema, len(req.Tools))
		for i, schema := range req.Tools {
			out.Tools[i] = schema
			if len(schema.Parameters) > 0 {
				out.Tools[i].Parameters = append(json.RawMessage(nil), schema.Parameters...)
			}
		}
	}
	if req.Temperature != nil {
		t := *req.Temperature
		out.Temperature = &t
	}
	if req.ResponseFormat != nil {
		rf := *req.ResponseFormat
		out.ResponseFormat = &rf
	}
	return out
}
