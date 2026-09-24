package model

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Wire message types.
const (
	WireHello         = "hello"
	WireHerdrStatus   = "herdr_status"
	WireSnapshot      = "snapshot"
	WireAgentUpdate   = "agent_update"
	WireAgentRemoved  = "agent_removed"
	WireHistoryItem   = "history_item"
	WireCommand       = "command"
	WireCommandResult = "command_result"
	WireResync        = "resync"
)

// ErrUnknownType indicates an unrecognised wire message type.
var ErrUnknownType = errors.New("unknown wire message type")

// HelloMsg is the handshake message sent by the bridge on WebSocket connection.
type HelloMsg struct {
	Type          string `json:"type"` // "hello"
	Version       string `json:"version"`
	Host          string `json:"host"`
	HerdrVersion  string `json:"herdr_version"`
	HerdrProtocol int    `json:"herdr_protocol"`
	HerdrOnline   bool   `json:"herdr_online"`
}

// HerdrStatusMsg notifies the relay when the bridge's connection to herdr changes.
type HerdrStatusMsg struct {
	Type        string `json:"type"` // "herdr_status"
	HerdrOnline bool   `json:"herdr_online"`
}

// SnapshotMsg transmits the full set of active agents from bridge to relay.
type SnapshotMsg struct {
	Type   string       `json:"type"` // "snapshot"
	Agents []AgentState `json:"agents"`
}

// AgentUpdateMsg notifies the relay of an added or modified agent.
type AgentUpdateMsg struct {
	Type  string     `json:"type"` // "agent_update"
	Agent AgentState `json:"agent"`
}

// AgentRemovedMsg notifies the relay that a pane closed or lost its agent.
type AgentRemovedMsg struct {
	Type   string `json:"type"` // "agent_removed"
	PaneID string `json:"pane_id"`
}

// HistoryItemMsg delivers a completed turn to the relay.
type HistoryItemMsg struct {
	Type string      `json:"type"` // "history_item"
	Item HistoryItem `json:"item"`
}

// CommandMsg is forwarded from the relay to the bridge to perform an action.
type CommandMsg struct {
	Type        string `json:"type"`       // "command"
	RequestID   string `json:"request_id"` // random 16 hex chars
	Action      string `json:"action"`     // "prompt" | "answer" | "cancel"
	PaneID      string `json:"pane_id"`
	ExpectedSeq uint64 `json:"expected_seq"`
	Text        string `json:"text,omitempty"`
	OptionID    string `json:"option_id,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

// CommandResultMsg reports command execution outcome from bridge to relay.
type CommandResultMsg struct {
	Type      string `json:"type"` // "command_result"
	RequestID string `json:"request_id"`
	OK        bool   `json:"ok"`
	ErrorCode string `json:"error_code,omitempty"`
	Message   string `json:"message,omitempty"`
}

// ResyncMsg asks the bridge to re-send its full snapshot.
type ResyncMsg struct {
	Type string `json:"type"` // "resync"
}

// DecodeWire parses a raw JSON WebSocket frame into a concrete message struct.
func DecodeWire(data []byte) (any, error) {
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, fmt.Errorf("decode wire type: %w", err)
	}

	switch header.Type {
	case WireHello:
		var msg HelloMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, fmt.Errorf("decode hello: %w", err)
		}
		return msg, nil
	case WireHerdrStatus:
		var msg HerdrStatusMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, fmt.Errorf("decode herdr_status: %w", err)
		}
		return msg, nil
	case WireSnapshot:
		var msg SnapshotMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, fmt.Errorf("decode snapshot: %w", err)
		}
		return msg, nil
	case WireAgentUpdate:
		var msg AgentUpdateMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, fmt.Errorf("decode agent_update: %w", err)
		}
		return msg, nil
	case WireAgentRemoved:
		var msg AgentRemovedMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, fmt.Errorf("decode agent_removed: %w", err)
		}
		return msg, nil
	case WireHistoryItem:
		var msg HistoryItemMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, fmt.Errorf("decode history_item: %w", err)
		}
		return msg, nil
	case WireCommand:
		var msg CommandMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, fmt.Errorf("decode command: %w", err)
		}
		return msg, nil
	case WireCommandResult:
		var msg CommandResultMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, fmt.Errorf("decode command_result: %w", err)
		}
		return msg, nil
	case WireResync:
		var msg ResyncMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, fmt.Errorf("decode resync: %w", err)
		}
		return msg, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownType, header.Type)
	}
}
