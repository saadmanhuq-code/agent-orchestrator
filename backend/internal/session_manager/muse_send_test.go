package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/muse"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type museOutputRuntime struct{ fakeRuntime }

func (r *museOutputRuntime) GetStyledOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error) {
	return r.GetOutput(ctx, handle, lines)
}

func TestSendMuseSubmissionEvidence(t *testing.T) {
	const rule = "──────────────────────────────────────────────────────────────────────────────"
	const footer = "muse-spark-1.3-contributor · max · workspace · Launch overrides"
	for _, tt := range []struct {
		name, output string
		readErr      error
		wantErr      bool
	}{
		{"stranded draft despite stale active status", rule + "\n❯ followup\ncontinued text\n" + rule + "\n" + footer, nil, true},
		{"accepted active empty composer", "◆ Thinking (49s · esc to interrupt)\n" + rule + "\n❯\n" + rule + "\n" + footer, nil, false},
		{"unknown frame", "unrecognized terminal", nil, true},
		{"idle empty composer is not acceptance proof", "◆ Done.\n" + rule + "\n❯\n" + rule + "\n" + footer, nil, true},
		{"output failure", "", errors.New("capture unavailable"), true},
		{"input picker receives no catch-up Enter", "◆ Request user input Verify\nEnter to select · Tab for an optional note · Esc to interrupt\n" + rule + "\n❯\n" + rule + "\n" + footer, nil, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeStore()
			st.sessions["s1"] = pastStartupGate(domain.SessionRecord{ID: "s1", Harness: domain.HarnessMuse, Activity: domain.Activity{State: domain.ActivityActive}, Metadata: domain.SessionMetadata{RuntimeHandleID: "s1"}})
			msg := &fakeMessenger{}
			m := newSendTestManager(t, muse.New(), msg, st)
			m.runtime = &museOutputRuntime{fakeRuntime{outputs: []string{tt.output}, outputErr: tt.readErr}}
			err := m.Send(context.Background(), "s1", "followup", nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Send error = %v, want error %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, ErrSendSubmissionUnconfirmed) {
				t.Fatalf("Send error = %v, want typed submission uncertainty", err)
			}
			if len(msg.msgs) != 1 || msg.msgs[0] != "followup" {
				t.Fatalf("writes = %#v, want only original message and no retries", msg.msgs)
			}
		})
	}
}
