package supervisor

import (
	"encoding/json"
	"strings"
	"time"
)

type Status struct {
	Phase          string    `json:"phase"`
	State          string    `json:"supervisor_state"`
	PID            int       `json:"pid"`
	UpdatedAt      time.Time `json:"updated_at"`
	LastEventAt    time.Time `json:"last_event_at"`
	EventCount     int       `json:"event_count"`
	ElapsedSeconds float64   `json:"elapsed_seconds"`
	Evidence       string    `json:"evidence"`
}

type Observation struct {
	State      string
	Action     string
	Reason     string
	UpdatedAt  time.Time
	LastEvent  time.Time
	EventCount int
}

type PolicyInput struct {
	Mode          string
	TimeoutSource string
	IdleEnabled   bool
	State         string
	Extensions    int
	MaxExtensions int
	Retries       int
	MaxRetries    int
	ChangedFiles  int
}

type PolicyDecision struct {
	Action string
	Reason string
}

// Decide is the safety gate for future active supervision. It has no side
// effects and encodes the invariants that explicit caller timeouts and
// implement jobs with side effects are never silently extended or retried.
func Decide(input PolicyInput) PolicyDecision {
	if input.State == "stalled" && input.IdleEnabled && strings.HasPrefix(input.TimeoutSource, "default:") && input.Extensions < input.MaxExtensions {
		return PolicyDecision{Action: "extend_idle", Reason: "default_idle_timeout_and_extension_budget_available"}
	}
	if input.State == "failed" && input.Mode == "inspect" && input.Retries < input.MaxRetries && input.ChangedFiles == 0 {
		return PolicyDecision{Action: "retry", Reason: "inspect_provider_failure_without_side_effects"}
	}
	return PolicyDecision{Action: "none", Reason: "safety_policy_no_automatic_mutation"}
}

// Observe classifies a worker using only durable status evidence and process
// liveness. It deliberately returns advisory actions; mutation belongs to the
// policy layer so shadow mode cannot affect a running worker.
func Observe(status Status, processAlive bool, now time.Time, stale time.Duration) Observation {
	result := Observation{State: "starting", Action: "none", Reason: "no_status_evidence", UpdatedAt: status.UpdatedAt, LastEvent: status.LastEventAt, EventCount: status.EventCount}
	if status.State != "" {
		result.State = status.State
	} else if status.Phase == "completed" {
		result.State = "completed"
	} else if status.Phase == "interrupted" {
		result.State = "interrupted"
	} else if status.Phase == "timeout" || status.Phase == "idle_timeout" || status.Phase == "provider_error" {
		result.State = "failed"
	} else if status.Phase != "" {
		result.State = "progressing"
	}
	if result.State == "completed" || result.State == "failed" || result.State == "interrupted" {
		result.Reason = "terminal_status"
		return result
	}
	if !processAlive {
		result.State = "failed"
		result.Reason = "process_not_alive_without_terminal_status"
		return result
	}
	if !status.UpdatedAt.IsZero() && stale > 0 && now.Sub(status.UpdatedAt) >= stale {
		result.State = "stalled"
		result.Reason = "status_heartbeat_stale"
		result.Action = "inspect"
		return result
	}
	if result.State == "starting" || result.State == "progressing" || result.State == "reasoning" || result.State == "executing" || result.State == "waiting" {
		result.Reason = "live_status"
		return result
	}
	result.State = "progressing"
	result.Reason = "live_process"
	return result
}

func Parse(data []byte) (Status, error) {
	var status Status
	if err := json.Unmarshal(data, &status); err != nil {
		return Status{}, err
	}
	return status, nil
}
