package llmrepair

import (
	"errors"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

func TestReasoningOnlyIsNotFinalOutput(t *testing.T) {
	err := AnalyzeMessage(llm.CompletionRequest{}, llm.Message{Content: []llm.ContentBlock{
		{Type: llm.BlockThinking, Thinking: "internal reasoning"},
	}}, "end_turn", llm.Usage{OutputTokens: 2000}, "qwen-test")
	var quality *QualityError
	if !errors.As(err, &quality) || quality.Issue != IssueEmpty {
		t.Fatalf("expected reasoning-only response error, got %v", err)
	}
	if !Retryable(err) || !FailoverEligible(err) {
		t.Fatalf("reasoning-only response should be repairable/failover eligible: %v", err)
	}
}

func TestAmbiguousEmptyIsNotCrossModelRetried(t *testing.T) {
	err := AnalyzeMessage(llm.CompletionRequest{}, llm.Message{}, "end_turn", llm.Usage{OutputTokens: 2000}, "qwen-test")
	var quality *QualityError
	if !errors.As(err, &quality) || quality.Issue != IssueAmbiguous {
		t.Fatalf("expected ambiguous empty response, got %v", err)
	}
	if Retryable(err) || FailoverEligible(err) {
		t.Fatalf("ambiguous response must not trigger retries/fallback: %v", err)
	}
}

func TestRefusalDoesNotTriggerRepairOrFallback(t *testing.T) {
	err := AnalyzeMessage(llm.CompletionRequest{}, llm.Message{}, "content_filter", llm.Usage{}, "test-model")
	var quality *QualityError
	if !errors.As(err, &quality) || quality.Issue != IssueModelRefusal {
		t.Fatalf("expected refusal classification, got %v", err)
	}
	if Retryable(err) || FailoverEligible(err) {
		t.Fatalf("refusal must not trigger retries/fallback: %v", err)
	}
}

func TestTruncatedResponseIsRejected(t *testing.T) {
	err := AnalyzeMessage(llm.CompletionRequest{}, llm.Message{Content: []llm.ContentBlock{
		llm.TextBlock("partial answer"),
	}}, "max_tokens", llm.Usage{OutputTokens: 100}, "test-model")
	var quality *QualityError
	if !errors.As(err, &quality) || quality.Issue != IssueTruncated {
		t.Fatalf("expected truncation classification, got %v", err)
	}
}

func TestEmptyTextToolCallIsAValidResponseOnlyWhenDeclared(t *testing.T) {
	call := llm.ContentBlock{Type: llm.BlockToolUse, ID: "call-1", Name: "Lookup", Input: []byte(`{"key":"x"}`)}
	req := llm.CompletionRequest{Tools: []llm.ToolSchema{{Name: "Lookup"}}}
	if err := AnalyzeMessage(req, llm.Message{Content: []llm.ContentBlock{call}}, "tool_use", llm.Usage{}, "test-model"); err != nil {
		t.Fatalf("declared tool call should be a valid output: %v", err)
	}
	if err := AnalyzeMessage(llm.CompletionRequest{}, llm.Message{Content: []llm.ContentBlock{call}}, "tool_use", llm.Usage{}, "test-model"); err == nil {
		t.Fatal("undeclared tool call must be rejected")
	}
}

func TestIsRefusalTextBlocksReviewerFormatRetry(t *testing.T) {
	if !IsRefusalText("I’m sorry, but I can’t assist with that request.") {
		t.Fatal("explicit refusal should be detected")
	}
	if IsRefusalText("The target cannot help us without authentication.") {
		t.Fatal("ordinary text mentioning inability is not a refusal opening")
	}
}

func TestStreamInspectorRejectsThinkingOnlyAndMissingTerminal(t *testing.T) {
	req := llm.CompletionRequest{}
	thinking := NewStreamInspector(req, "test-model")
	thinking.Add(llm.StreamEvent{Type: llm.SEThinkingDelta, Text: "pondering"})
	thinking.Add(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"})
	var quality *QualityError
	if err := thinking.Analyze(); !errors.As(err, &quality) || quality.Issue != IssueEmpty {
		t.Fatalf("expected thinking-only stream error, got %v", err)
	}

	partial := NewStreamInspector(req, "test-model")
	partial.Add(llm.StreamEvent{Type: llm.SETextDelta, Text: "partial"})
	if err := partial.Analyze(); !errors.As(err, &quality) || quality.Issue != IssueIncomplete {
		t.Fatalf("expected incomplete stream error, got %v", err)
	}
}
