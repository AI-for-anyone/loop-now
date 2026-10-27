// Package pluginhost 监护插件宿主进程（plugins/SPEC.md §3）：拉起与就绪握手、平台半边的加载与卸载、
// 观察类事件的持久投递、同步调用的超时策略、崩溃重启与全局停用。线上格式见 api/plugin-host-protocol.md。
package pluginhost

import "encoding/json"

// ProtocolVersion 是宿主在 ready 里声明、Go 侧接受的协议版本。
const ProtocolVersion = 1

// 消息类型。Go→宿主：load、unload、config_changed、event、call、action、http、schedule、shutdown；
// 宿主→Go：ready、result、ack、log、state_get、state_set、state_delete、state_list。
// result 是任一方对另一方请求的回应；ack 只回应 event。
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

// MaxLine 是一行消息的最大字节数（含换行）。
const MaxLine = 16 << 20

// Message 是协议的一行：{"id","type","plugin","payload"}。
type Message struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Plugin  string          `json:"plugin,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Reply 是 result 与 ack 的载荷：成功时 ok 为 true 并可带 value，失败时带 error。
type Reply struct {
	OK    bool            `json:"ok"`
	Value json.RawMessage `json:"value,omitempty"`
	Error string          `json:"error,omitempty"`
}

// readyPayload 是宿主启动后第一行 ready 的载荷。
type readyPayload struct {
	Protocol    int    `json:"protocol"`
	HostVersion string `json:"hostVersion"`
}

// logPayload 是宿主 log 消息的载荷。
type logPayload struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// stateRequest 是 state_* 请求的载荷；projectId 为空表示插件全局。
type stateRequest struct {
	ProjectID *string         `json:"projectId"`
	Key       string          `json:"key"`
	Prefix    string          `json:"prefix"`
	Value     json.RawMessage `json:"value"`
}
