// Package llmrepair classifies normalized model responses and validates tool
// arguments before the agent runtime can execute them.
package llmrepair

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
)

type Issue string

const (
	IssueEmpty        Issue = "empty_response"
	IssueTruncated    Issue = "truncated_response"
	IssueIncomplete   Issue = "incomplete_stream"
	IssueInvalidTool  Issue = "invalid_tool_arguments"
	IssueModelRefusal Issue = "model_refusal"
	IssueAmbiguous    Issue = "ambiguous_empty_response"
)

// QualityError is a safe, bounded diagnostic. It intentionally records byte
// counts and protocol metadata, never model reasoning or tool argument values.
type QualityError struct {
	Issue          Issue
	Model          string
	StopReason     string
	TextBytes      int
	ReasoningBytes int
	ToolCalls      int
	OutputTokens   int
	Detail         string
}

func (e *QualityError) Error() string {
	if e == nil {
		return "model response quality error"
	}
	detail := ""
	if e.Detail != "" {
		detail = ", detail=" + e.Detail
	}
	return fmt.Sprintf("model response rejected: issue=%s model=%q stop_reason=%q text_bytes=%d reasoning_bytes=%d tool_calls=%d output_tokens=%d%s",
		e.Issue, e.Model, e.StopReason, e.TextBytes, e.ReasoningBytes, e.ToolCalls, e.OutputTokens, detail)
}

func IsQualityError(err error) bool {
	var q *QualityError
	return errors.As(err, &q)
}

func IsRefusal(err error) bool {
	var q *QualityError
	return errors.As(err, &q) && q.Issue == IssueModelRefusal
}

func Retryable(err error) bool {
	var q *QualityError
	return errors.As(err, &q) && q.Issue != IssueModelRefusal && q.Issue != IssueAmbiguous
}

// RepairRequest appends a host-controlled protocol correction without echoing
// untrusted tool arguments, user data, or hidden reasoning into the retry prompt.
func RepairRequest(req llm.CompletionRequest, err error) llm.CompletionRequest {
	var q *QualityError
	if !errors.As(err, &q) || q.Issue == IssueModelRefusal {
		return req
	}
	correction := "Protocol correction: the previous response was not complete and valid. Return a complete response using only the declared output format and tool schemas. Do not use hidden reasoning as final text."
	switch q.Issue {
	case IssueInvalidTool:
		correction = "Protocol correction: the previous tool arguments did not satisfy the declared schema. Regenerate a schema-valid tool call or return a final response; do not repeat invalid arguments."
	case IssueTruncated:
		correction = "Protocol correction: the previous response was truncated. Return a complete response within the available output limit."
	case IssueIncomplete:
		correction = "Protocol correction: the previous response did not finish its protocol turn. Return a complete response."
	case IssueEmpty:
		correction = "Protocol correction: the previous response contained no final text or complete tool call. Return a non-empty final response or a valid tool call."
	}
	out := req
	out.System = append(append([]string(nil), req.System...), correction)
	return out
}

func FailoverEligible(err error) bool {
	var q *QualityError
	return errors.As(err, &q) && q.Issue != IssueModelRefusal && q.Issue != IssueAmbiguous
}

// IsRefusalText recognizes common explicit refusal openings for consumers whose
// output contract has a second-stage parser (for example, the Reviewer JSON
// contract). It is intentionally conservative: a match suppresses repair/fallback
// rather than changing or discarding the model's text.
func IsRefusalText(text string) bool {
	line := strings.TrimSpace(strings.ToLower(text))
	if end := strings.IndexAny(line, "\r\n"); end >= 0 {
		line = strings.TrimSpace(line[:end])
	}
	line = strings.TrimLeft(line, " \t\"'`*_>")
	if len(line) > 320 {
		line = line[:320]
	}
	for _, prefix := range []string{
		"i cannot help", "i can't help", "i can’t help", "i cannot assist", "i can't assist", "i can’t assist",
		"i am unable to", "i'm unable to", "i’m unable to", "i cannot provide", "i can't provide", "i can’t provide",
		"i cannot comply", "i can't comply", "i can’t comply", "i must refuse", "i'm sorry, but i cannot",
		"i'm sorry, but i can't", "i’m sorry, but i can’t", "as an ai, i cannot", "as an ai language model",
		"抱歉，我无法", "抱歉，我不能", "抱歉，无法", "抱歉，不能",
		"我无法帮助", "我不能帮助", "我无法协助", "我不能协助",
		"无法帮助您", "无法协助您", "不能帮助您", "不能协助您",
	} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// AnalyzeMessage classifies a complete normalized response. A valid tool call
// is a successful response even when assistant text is empty.
func AnalyzeMessage(req llm.CompletionRequest, msg llm.Message, stopReason string, usage llm.Usage, model string) error {
	q := &QualityError{Model: model, StopReason: strings.TrimSpace(stopReason), OutputTokens: usage.OutputTokens}
	var toolBlocks []llm.ContentBlock
	for _, block := range msg.Content {
		switch block.Type {
		case llm.BlockText:
			if strings.TrimSpace(block.Text) != "" {
				q.TextBytes += len(block.Text)
			}
		case llm.BlockThinking:
			q.ReasoningBytes += len(block.Thinking)
		case llm.BlockToolUse:
			q.ToolCalls++
			toolBlocks = append(toolBlocks, block)
		}
	}
	if isRefusalStop(q.StopReason) {
		if q.ToolCalls > 0 || q.TextBytes == 0 {
			q.Issue = IssueModelRefusal
			return q
		}
		return nil // preserve an explicit refusal carried as ordinary text
	}
	if isTruncatedStop(q.StopReason) {
		q.Issue = IssueTruncated
		return q
	}
	for _, block := range toolBlocks {
		if err := validateTool(req.Tools, block.Name, block.Input); err != nil {
			q.Issue, q.Detail = IssueInvalidTool, err.Error()
			return q
		}
	}
	if q.TextBytes == 0 && q.ToolCalls == 0 {
		if q.ReasoningBytes == 0 && q.OutputTokens > 0 {
			q.Issue = IssueAmbiguous
			q.Detail = "usage reports generated tokens but normalized text, reasoning, and tool fields are empty; refusing cross-model retry because refusal or adapter loss cannot be distinguished"
		} else {
			q.Issue = IssueEmpty
			if q.ReasoningBytes > 0 {
				q.Detail = "reasoning-only response; no final text or tool call"
			} else {
				q.Detail = "normalized response and reported output usage are empty; raw envelope unavailable, upstream vacuum not proven"
			}
		}
		return q
	}
	return nil
}

// StreamInspector accumulates only classification metadata and tool JSON; it
// never retains or exposes reasoning text.
type StreamInspector struct {
	request        llm.CompletionRequest
	model          string
	textBytes      int
	reasoningBytes int
	stopReason     string
	terminal       bool
	usage          llm.Usage
	tools          []*streamTool
	byID           map[string]*streamTool
	currentToolID  string
}

type streamTool struct {
	id   string
	name string
	args strings.Builder
}

func NewStreamInspector(req llm.CompletionRequest, model string) *StreamInspector {
	return &StreamInspector{request: req, model: model, byID: make(map[string]*streamTool)}
}

func (i *StreamInspector) Add(ev llm.StreamEvent) {
	if i == nil {
		return
	}
	switch ev.Type {
	case llm.SETextDelta:
		if strings.TrimSpace(ev.Text) != "" {
			i.textBytes += len(ev.Text)
		}
	case llm.SEThinkingDelta:
		i.reasoningBytes += len(ev.Text)
	case llm.SEToolUseStart:
		id := strings.TrimSpace(ev.ToolID)
		if id == "" {
			id = fmt.Sprintf("anonymous-%d", len(i.tools)+1)
		}
		t := &streamTool{id: id, name: ev.ToolName}
		i.tools = append(i.tools, t)
		i.byID[id] = t
		i.currentToolID = id
	case llm.SEToolInputJSON:
		id := strings.TrimSpace(ev.ToolID)
		if id == "" {
			id = i.currentToolID
		}
		if t := i.byID[id]; t != nil {
			t.args.WriteString(ev.Text)
		}
	case llm.SEMessageDelta:
		if strings.TrimSpace(ev.StopReason) != "" {
			i.stopReason = strings.TrimSpace(ev.StopReason)
			i.terminal = true
		}
		if ev.Usage.OutputTokens > i.usage.OutputTokens {
			i.usage = ev.Usage
		}
	case llm.SEMessageStart:
		if ev.Usage.OutputTokens > i.usage.OutputTokens {
			i.usage = ev.Usage
		}
	case llm.SEMessageStop:
		i.terminal = true
	}
}

func (i *StreamInspector) Analyze() error {
	if i == nil {
		return &QualityError{Issue: IssueIncomplete, Detail: "missing response inspector"}
	}
	q := &QualityError{
		Model: i.model, StopReason: i.stopReason, TextBytes: i.textBytes,
		ReasoningBytes: i.reasoningBytes, ToolCalls: len(i.tools), OutputTokens: i.usage.OutputTokens,
	}
	if isRefusalStop(q.StopReason) {
		if q.ToolCalls > 0 || q.TextBytes == 0 {
			q.Issue = IssueModelRefusal
			return q
		}
		return nil // preserve an explicit refusal carried as ordinary text
	}
	if isTruncatedStop(q.StopReason) {
		q.Issue = IssueTruncated
		return q
	}
	if !i.terminal {
		q.Issue = IssueIncomplete
		return q
	}
	for _, t := range i.tools {
		if err := validateTool(i.request.Tools, t.name, []byte(t.args.String())); err != nil {
			q.Issue, q.Detail = IssueInvalidTool, err.Error()
			return q
		}
	}
	if q.TextBytes == 0 && q.ToolCalls == 0 {
		if q.ReasoningBytes == 0 && q.OutputTokens > 0 {
			q.Issue = IssueAmbiguous
			q.Detail = "usage reports generated tokens but normalized text, reasoning, and tool fields are empty; refusing cross-model retry because refusal or adapter loss cannot be distinguished"
		} else {
			q.Issue = IssueEmpty
			if q.ReasoningBytes > 0 {
				q.Detail = "reasoning-only response; no final text or tool call"
			} else {
				q.Detail = "normalized response and reported output usage are empty; raw envelope unavailable, upstream vacuum not proven"
			}
		}
		return q
	}
	return nil
}

func validateTool(schemas []llm.ToolSchema, name string, raw json.RawMessage) error {
	var schema map[string]any
	found := false
	for _, candidate := range schemas {
		if candidate.Name == name {
			schema, found = candidate.InputSchema, true
			break
		}
	}
	if !found {
		return fmt.Errorf("tool %q was not present in the request schema", name)
	}
	trimmed := strings.TrimSpace(string(raw))
	input := map[string]any{}
	if trimmed != "" && trimmed != "null" {
		if !json.Valid(raw) {
			return fmt.Errorf("tool %q arguments are not complete JSON", name)
		}
		if err := json.Unmarshal(raw, &input); err != nil || input == nil {
			return fmt.Errorf("tool %q arguments must be a JSON object", name)
		}
	}
	// Skill inputs are intentionally validated by the Skill dispatcher, whose
	// schema is contextual and may be augmented after discovery.
	if name != "Skill" && len(schema) > 0 {
		raw, _ := json.Marshal(input)
		if err := actool.ValidateInput(schema, raw); err != nil {
			return fmt.Errorf("tool %q arguments do not match its declared schema", name)
		}
	}
	return nil
}

func isTruncatedStop(reason string) bool {
	r := strings.ToLower(strings.TrimSpace(reason))
	for _, marker := range []string{"length", "max_tokens", "max_output_tokens", "token_limit", "incomplete"} {
		if strings.Contains(r, marker) {
			return true
		}
	}
	return false
}

func isRefusalStop(reason string) bool {
	r := strings.ToLower(strings.TrimSpace(reason))
	for _, marker := range []string{"refusal", "content_filter", "safety", "recitation", "blocked", "prohibited_content", "blocklist"} {
		if strings.Contains(r, marker) {
			return true
		}
	}
	return false
}
