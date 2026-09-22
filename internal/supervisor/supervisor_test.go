package supervisor

import (
	"testing"
	"time"
)

func TestObserveUsesHeartbeatForLiveReasoning(t *testing.T) {
	now := time.Now()
	result := Observe(Status{State: "reasoning", UpdatedAt: now.Add(-2 * time.Second), LastEventAt: now.Add(-30 * time.Second), EventCount: 4}, true, now, 5*time.Second)
	if result.State != "reasoning" || result.Action != "none" {
		t.Fatalf("result=%+v", result)
	}
}

func TestObserveMarksStaleLiveProcess(t *testing.T) {
	now := time.Now()
	result := Observe(Status{State: "progressing", UpdatedAt: now.Add(-6 * time.Second)}, true, now, 5*time.Second)
	if result.State != "stalled" || result.Action != "inspect" || result.Reason != "status_heartbeat_stale" {
		t.Fatalf("result=%+v", result)
	}
}

func TestObserveDoesNotCallDeadProcessStalled(t *testing.T) {
	now := time.Now()
	result := Observe(Status{State: "progressing", UpdatedAt: now.Add(-20 * time.Second)}, false, now, 5*time.Second)
	if result.State != "failed" || result.Action != "none" {
		t.Fatalf("result=%+v", result)
	}
}

func TestObservePreservesTerminalStatus(t *testing.T) {
	now := time.Now()
	result := Observe(Status{Phase: "completed", State: "completed", UpdatedAt: now.Add(-20 * time.Second)}, true, now, time.Second)
	if result.State != "completed" || result.Reason != "terminal_status" {
		t.Fatalf("result=%+v", result)
	}
}

func TestDecideExtendsOnlyDefaultIdleBudget(t *testing.T) {
	decision := Decide(PolicyInput{TimeoutSource: "default:inspect", IdleEnabled: true, State: "stalled", Extensions: 0, MaxExtensions: 2})
	if decision.Action != "extend_idle" {
		t.Fatalf("decision=%+v", decision)
	}
	if explicit := Decide(PolicyInput{TimeoutSource: "explicit", IdleEnabled: true, State: "stalled", Extensions: 0, MaxExtensions: 2}); explicit.Action != "none" {
		t.Fatalf("explicit timeout decision=%+v", explicit)
	}
}

func TestDecideRetriesOnlyCleanInspectFailure(t *testing.T) {
	decision := Decide(PolicyInput{Mode: "inspect", State: "failed", Retries: 0, MaxRetries: 1})
	if decision.Action != "retry" {
		t.Fatalf("decision=%+v", decision)
	}
	for _, input := range []PolicyInput{
		{Mode: "implement", State: "failed", Retries: 0, MaxRetries: 1},
		{Mode: "inspect", State: "failed", Retries: 0, MaxRetries: 1, ChangedFiles: 1},
		{Mode: "inspect", State: "failed", Retries: 1, MaxRetries: 1},
	} {
		if got := Decide(input); got.Action != "none" {
			t.Fatalf("unsafe retry decision=%+v input=%+v", got, input)
		}
	}
}
