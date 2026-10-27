// Package taskruntime 提供离线的运行作用域与不可变任务配置快照。
// 它不读取当前配置、不持久化数据、不启动执行，也不作授权判断。
package taskruntime

import (
	"errors"

	"github.com/google/uuid"
)

var (
	ErrInvalidInput        = errors.New("taskruntime: invalid input")
	ErrInvalidEncoding     = errors.New("taskruntime: invalid encoding")
	ErrUnsupportedVersion  = errors.New("taskruntime: unsupported version")
	ErrCredentials         = errors.New("taskruntime: credentials unavailable")
	ErrUnsafeSerialization = errors.New("taskruntime: use explicit snapshot encoding")
)

// Scope 是资源命名空间，不是身份凭证或角色。零值无效。
type Scope struct {
	agentID uuid.UUID
	taskID  uuid.UUID
}

func NormalScope(agentID uuid.UUID) (Scope, error) {
	if agentID == uuid.Nil {
		return Scope{}, ErrInvalidInput
	}
	return Scope{agentID: agentID}, nil
}

func TaskScope(agentID, taskID uuid.UUID) (Scope, error) {
	if agentID == uuid.Nil || taskID == uuid.Nil {
		return Scope{}, ErrInvalidInput
	}
	return Scope{agentID: agentID, taskID: taskID}, nil
}

func (s Scope) AgentID() uuid.UUID { return s.agentID }
func (s Scope) TaskID() uuid.UUID  { return s.taskID }
func (s Scope) IsTask() bool       { return s.taskID != uuid.Nil }
func (s Scope) Valid() bool        { return s.agentID != uuid.Nil }

func (s Scope) Key() string {
	if !s.Valid() {
		return "invalid"
	}
	if !s.IsTask() {
		return "normal/" + s.agentID.String()
	}
	return "task/" + s.taskID.String() + "/agent/" + s.agentID.String()
}

func (s Scope) String() string { return s.Key() }

type scopeWire struct {
	AgentID uuid.UUID `json:"agentId"`
	TaskID  uuid.UUID `json:"taskId"`
}

func (s Scope) wire() scopeWire { return scopeWire{s.agentID, s.taskID} }
