package relay

import "time"

// Snapshot is a point-in-time Relay runtime view for the admin panel.
type Snapshot struct {
	Connections      int               `json:"connections"`
	Waiting          int               `json:"waiting"`
	Active           int               `json:"active"`
	TotalSourceBytes uint64            `json:"total_source_bytes"`
	TotalTargetBytes uint64            `json:"total_target_bytes"`
	Sessions         []SessionSnapshot `json:"sessions"`
}

// SessionSnapshot describes one waiting or active Relay session.
type SessionSnapshot struct {
	SessionID      string    `json:"session_id"`
	State          string    `json:"state"`
	SourceRemote   string    `json:"source_remote,omitempty"`
	TargetRemote   string    `json:"target_remote,omitempty"`
	SourceToTarget uint64    `json:"source_to_target"`
	TargetToSource uint64    `json:"target_to_source"`
	StartedAt      time.Time `json:"started_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	CompletedAt    time.Time `json:"completed_at,omitempty"`
	CloseReason    string    `json:"close_reason,omitempty"`
}

type sessionStats struct {
	sessionID      string
	state          string
	sourceRemote   string
	targetRemote   string
	sourceToTarget uint64
	targetToSource uint64
	startedAt      time.Time
	updatedAt      time.Time
	completedAt    time.Time
	closeReason    string
}
