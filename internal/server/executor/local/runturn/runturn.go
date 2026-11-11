// Package runturn 是本机执行器与各运行时客户端（Pi RPC、Claude Code）之间共用的"一轮"词汇：
// 实时事件的接收方、一轮的结果与结局，以及进程已退出的错误。执行器只按这些类型处理一轮，不关心是哪个运行时。
package runturn

import "errors"

// ErrExited 表示运行时进程已退出或其 stdout 已关闭。
var ErrExited = errors.New("pirpc: Pi 进程已退出")

// Sink 接收一轮执行的实时事件。调用发生在客户端的读协程上，实现必须迅速返回，
// 且不得在回调中同步调用客户端的方法。
type Sink interface {
	TextDelta(delta string)
	ToolStarted(toolCallID, toolName string)
	ToolEnded(toolCallID, toolName string, isError bool)
}

// Outcome 是一轮执行的结局。
type Outcome string

const (
	OutcomeCompleted Outcome = "completed"
	OutcomeFailed    Outcome = "failed"
	OutcomeAborted   Outcome = "aborted"
)

// Result 是一轮执行的结果。
type Result struct {
	Outcome      Outcome
	Text         string // 最终回复文本
	ErrorCode    string // 失败原因（RunErrorCode），成功时为空
	ErrorMessage string
	// Handled 表示运行时直接处理了这条消息（Pi 的扩展命令），没有交给模型、没有回复。
	Handled bool
	// Notices 是 Handled 时这一轮里扩展用 ctx.ui.notify 给出的消息（命令的输出）；指针让 Result 仍可比较。
	Notices *CommandNotices
}

// CommandNotices 是一轮里收集到的通知，按顺序；Truncated 表示超过上限被截断。
type CommandNotices struct {
	Items     []CommandNotice
	Truncated bool
}

// CommandNotice 是扩展在这一轮里用 ctx.ui.notify 给出的一条消息（扩展原文，纯文本）。
type CommandNotice struct {
	Level   string `json:"level"` // info / warning / error
	Message string `json:"message"`
}
