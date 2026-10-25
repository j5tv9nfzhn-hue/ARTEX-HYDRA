package agent

import (
	"context"
	"iter"
	"log"
	"strings"
	"time"

	"github.com/Autumn-27/artex/llmrepair"
	"github.com/Autumn-27/norma/llm"
)

const responseRepairAttempts = 1

// responseGuardProvider is installed under every configured provider. It only
// retries normalized, locally-classified output defects; provider refusals and
// caller cancellation are never retried. Tool events stay buffered until their
// complete arguments have passed local schema validation.
type responseGuardProvider struct {
	inner llm.Provider
	model string
}

func guardProvider(inner llm.Provider, model string) llm.Provider {
	if inner == nil {
		return nil
	}
	return &responseGuardProvider{inner: inner, model: model}
}

func (p *responseGuardProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	current := req
	for attempt := 0; ; attempt++ {
		msg, stopReason, usage, err := p.inner.Complete(ctx, current)
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		qualityErr := llmrepair.AnalyzeMessage(current, msg, stopReason, usage, p.model)
		if qualityErr == nil {
			return msg, stopReason, usage, nil
		}
		if !llmrepair.Retryable(qualityErr) || attempt >= responseRepairAttempts {
			return llm.Message{}, stopReason, usage, qualityErr
		}
		log.Printf("[llm-repair] retrying model %q after normalized output validation: %v", p.model, qualityErr)
		if !waitResponseRepair(ctx) {
			return llm.Message{}, "", llm.Usage{}, ctx.Err()
		}
		current = llmrepair.RepairRequest(current, qualityErr)
	}
}

func (p *responseGuardProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		current := req
		for attempt := 0; ; attempt++ {
			inspector := llmrepair.NewStreamInspector(current, p.model)
			var pending []llm.StreamEvent
			committedText := false
			sawToolEvent := false
			var streamErr error

			for event, err := range p.inner.Stream(ctx, current) {
				if err != nil {
					streamErr = err
					break
				}
				inspector.Add(event)
				if event.Type == llm.SEToolUseStart || event.Type == llm.SEToolInputJSON {
					sawToolEvent = true
				}
				if sawToolEvent {
					pending = append(pending, event)
					continue
				}
				if event.Type == llm.SETextDelta && strings.TrimSpace(event.Text) != "" {
					if !committedText {
						for _, buffered := range pending {
							if !yield(buffered, nil) {
								return
							}
						}
						pending = nil
						committedText = true
					}
					if !yield(event, nil) {
						return
					}
					continue
				}
				if committedText && event.Type != llm.SEMessageDelta && event.Type != llm.SEMessageStop {
					if !yield(event, nil) {
						return
					}
				} else {
					pending = append(pending, event)
				}
			}

			if streamErr != nil {
				// Do not expose reasoning or an incomplete tool call as an answer.
				// Once final text has been streamed, surface the error rather than
				// replaying a second model response into the same conversation.
				yield(llm.StreamEvent{}, streamErr)
				return
			}

			qualityErr := inspector.Analyze()
			if qualityErr != nil {
				if !committedText && llmrepair.Retryable(qualityErr) && attempt < responseRepairAttempts {
					log.Printf("[llm-repair] retrying model %q after streamed output validation: %v", p.model, qualityErr)
					if !waitResponseRepair(ctx) {
						yield(llm.StreamEvent{}, ctx.Err())
						return
					}
					current = llmrepair.RepairRequest(current, qualityErr)
					continue
				}
				yield(llm.StreamEvent{}, qualityErr)
				return
			}

			// A tool-only response is released only after the full protocol turn
			// has arrived and every argument has passed local validation.
			for _, buffered := range pending {
				if !yield(buffered, nil) {
					return
				}
			}
			return
		}
	}
}

func waitResponseRepair(ctx context.Context) bool {
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}
