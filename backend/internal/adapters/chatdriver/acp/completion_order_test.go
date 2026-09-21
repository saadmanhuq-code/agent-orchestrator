package acp

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	acpsdk "github.com/coder/acp-go-sdk"
)

func TestCompletionAllowsImmediateNextTurn(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "completed"
		if failed {
			name = "failed"
		}
		t.Run(name, func(t *testing.T) {
			// An unbuffered stream keeps finishPrompt at its next emission while
			// the consumer submits immediately on the completion event.
			c := &conversation{sessionID: "session", activeTurn: "previous", events: make(chan ports.ChatEvent)}
			done := make(chan struct{})
			go func() {
				defer close(done)
				var promptErr error
				if failed {
					promptErr = errors.New("provider rejected prompt")
				}
				c.finishPrompt("previous", acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, promptErr)
			}()
			// A failed turn carries the error on its completion event; no separate
			// error event precedes it.
			event := nextEvent(t, c.Events())
			if event.Kind != ports.ChatEventTurnCompleted {
				t.Fatalf("event = %s, want turn completed", event.Kind)
			}
			if failed && (event.TurnState != domain.TurnStateFailed || event.Err == nil) {
				t.Errorf("completion = state %s err %v, want failed turn carrying the error", event.TurnState, event.Err)
			}
			ref, err := c.SendTurn(context.Background(), ports.ChatUserMessage{Text: "next"})
			if err != nil {
				t.Errorf("submit immediately after completion: %v", err)
			}
			if event := nextEvent(t, c.Events()); event.Kind != ports.ChatEventControllerState || event.ControllerState != ports.ChatControllerReady {
				t.Errorf("event = %#v, want ready", event)
			}
			<-done
			if err == nil && (c.prepared == nil || c.prepared.id != ref.ProviderTurnID) {
				t.Error("completion cleanup discarded the next prepared turn")
			}
		})
	}
}
