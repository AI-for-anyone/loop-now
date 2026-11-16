// Package pluginhost supervises the plugin host process (plugins/SPEC.md §3): launching it and the ready handshake,
// loading and unloading platform sides, durable delivery of observe events, the timeout policy of synchronous calls,
// restarting after crashes, and disabling globally. The wire format is in api/plugin-host-protocol.md.
package pluginhost

import "encoding/json"

// ProtocolVersion is the protocol version the plugin host declares in ready and the Go side accepts.
const ProtocolVersion = 1

// Message types. Go -> plugin host: load, unload, config_changed, event, call, action, http, schedule, shutdown;
// plugin host -> Go: ready, result, ack, log, state_get, state_set, state_delete, state_list.
// result is either side's response to a request from the other side; ack only answers event.
const (
	TypeReady         = "ready"
	TypeResult        = "result"
	TypeAck           = "ack"
	TypeLog           = "log"
	TypeStateGet      = "state_get"
	TypeStateSet      = "state_set"
	TypeStateDelete   = "state_delete"
	TypeStateList     = "state_list"
	TypeAccountsList  = "accounts_list"
	TypeLoad          = "load"
	TypeUnload        = "unload"
	TypeConfigChanged = "config_changed"
	TypeEvent         = "event"
	TypeCall          = "call"
	TypeAction        = "action"
	TypeHTTP          = "http"
	TypeSchedule      = "schedule"
	TypeShutdown      = "shutdown"
)

// MaxLine is the maximum size in bytes of one message line (including the newline).
const MaxLine = 16 << 20

// Message is one line of the protocol: {"id","type","plugin","payload"}.
type Message struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Plugin  string          `json:"plugin,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Reply is the payload of result and ack: on success ok is true and value may be set; on failure error is set.
type Reply struct {
	OK    bool            `json:"ok"`
	Value json.RawMessage `json:"value,omitempty"`
	Error string          `json:"error,omitempty"`
}

// readyPayload is the payload of ready, the first line the plugin host sends after starting.
type readyPayload struct {
	Protocol    int    `json:"protocol"`
	HostVersion string `json:"hostVersion"`
}

// logPayload is the payload of the plugin host's log message.
type logPayload struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// stateRequest is the payload of state_* requests; an empty projectId means plugin-global state.
type stateRequest struct {
	ProjectID *string         `json:"projectId"`
	Key       string          `json:"key"`
	Prefix    string          `json:"prefix"`
	Value     json.RawMessage `json:"value"`
}
