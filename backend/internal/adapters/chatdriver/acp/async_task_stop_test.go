package acp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestStopAsyncTaskSendsTheSessionAndTaskID(t *testing.T) {
	agent := &fakeAgent{stopTaskSupported: true, stopTaskStopped: true}
	conv := openCompactionConversation(t, agent)

	stopped, err := conv.StopAsyncTask(context.Background(), "bg-7")
	if err != nil || !stopped {
		t.Fatalf("stopped = %v, err = %v; want stopped", stopped, err)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.stopTaskCalls) != 1 {
		t.Fatalf("calls = %d, want 1", len(agent.stopTaskCalls))
	}
	var got struct {
		SessionID   string `json:"sessionId"`
		AsyncTaskID string `json:"asyncTaskId"`
	}
	_ = json.Unmarshal(agent.stopTaskCalls[0], &got)
	if got.SessionID == "" || got.AsyncTaskID != "bg-7" {
		t.Fatalf("payload = %+v, want a session id and bg-7", got)
	}
}

// A bridge that has no such request, or that declines because the task ended,
// is "not stopped", not a failure.
func TestStopAsyncTaskDeclinedOrUnsupportedIsNotAnError(t *testing.T) {
	for name, agent := range map[string]*fakeAgent{
		"unsupported": {stopTaskSupported: false},
		"declined":    {stopTaskSupported: true, stopTaskStopped: false},
	} {
		t.Run(name, func(t *testing.T) {
			conv := openCompactionConversation(t, agent)
			if stopped, err := conv.StopAsyncTask(context.Background(), "bg-7"); err != nil || stopped {
				t.Fatalf("stopped = %v, err = %v; want false, nil", stopped, err)
			}
		})
	}
}

func TestStopAsyncTaskOnAClosedConversationErrors(t *testing.T) {
	conv := openCompactionConversation(t, &fakeAgent{stopTaskSupported: true, stopTaskStopped: true})
	_ = conv.Close()
	if _, err := conv.StopAsyncTask(context.Background(), "bg-7"); err == nil {
		t.Fatal("want an error on a closed conversation")
	}
}
