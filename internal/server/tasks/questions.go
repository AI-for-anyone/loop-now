package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
)

// OpenQuestion 供统一问题服务接入：在同一事务中创建原题并绑定当前节点，保证一节点一未回答问题。
// create只能写问题/Inbox数据，不能回调本服务再次获取任务锁。
func (s *Service) OpenQuestion(ctx context.Context, p auth.Principal, nodeID uuid.UUID, create func(pgx.Tx) (taskmodel.Question, error)) (json.RawMessage, error) {
	return s.Store.MutateTask(ctx, p.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		if e := nodeActor(p, m, nodeID); e != nil {
			return nil, e
		}
		if e := s.Store.CheckTaskLarkRunTx(ctx, m, nodeID, p.RuntimeID, uuid.Nil); e != nil {
			return nil, e
		}
		n := m.Record.Current()
		if m.Record.Ended() || n.Status == "blocked" || n.Output != nil {
			return nil, conflict("当前节点不能发起问题")
		}
		if n.Question != nil && n.Question.Status == "open" {
			return nil, conflict("当前节点已有未回答的问题")
		}
		q, e := create(m.Tx)
		if e != nil {
			return nil, e
		}
		if q.ID == uuid.Nil || q.ThreadID == uuid.Nil || q.MessageID == uuid.Nil || q.Status != "open" {
			return nil, apperr.Invalid("question", "统一问题服务返回的原题引用无效")
		}
		q.CanAnswer = false // 权限按读取方即时计算，不能把创建者或控制台权限缓存给其他身份。
		n.Question = &q
		return q, m.Emit("node.updated", map[string]any{"node": n})
	})
}

// QuestionChanged 由统一问题服务提交后通知，可重复执行。迟到旧题只更新历史，不唤醒新节点或结束任务。
func (s *Service) QuestionChanged(ctx context.Context, taskID, questionID uuid.UUID, status string) error {
	if status != "answered" && status != "expired" && status != "closed" {
		return apperr.Invalid("status", "问题终态无效")
	}
	_, err := s.Store.MutateTask(ctx, taskID, nil, func(m *store.TaskMutation) (any, error) {
		var node *taskmodel.NodeRun
		for i := range m.Record.View.Nodes {
			n := &m.Record.View.Nodes[i]
			if n.Question != nil && n.Question.ID == questionID {
				node = n
				break
			}
		}
		if node == nil || node.Question.Status != "open" {
			m.Unchanged()
			return nil, nil
		}
		node.Question.Status = status
		node.Question.CanAnswer = false
		if !m.Record.Ended() && m.Record.Current() != nil && m.Record.Current().ID == node.ID && node.Status != "blocked" {
			state := map[string]string{"answered": "已回答", "expired": "已到期", "closed": "已关闭"}[status]
			text := fmt.Sprintf("你发起的问题 %s %s。请从Inbox读取原题及回复后继续当前节点，不把普通留言当作选项答案。", questionID, state)
			turn, e := s.Store.EnqueueTaskTurn(ctx, m, text, "inbox_reminder", 1, &node.Question.MessageID)
			if e != nil {
				return nil, e
			}
			if e = store.MarkQuestionWakeupTx(ctx, m.Tx, turn.ID); e != nil {
				return nil, e
			}
		}
		return nil, m.Emit("node.updated", map[string]any{"node": node})
	})
	if err == nil {
		s.Kick(taskID)
	}
	return err
}

// QuestionViews从统一问题服务按当前读取方取得可答权限；未接入时安全地显示不可回答。
func (s *Service) QuestionViews(ctx context.Context, p auth.Principal, d taskmodel.Detail) (taskmodel.Detail, error) {
	for i := range d.Nodes {
		if q := d.Nodes[i].Question; q != nil {
			view, e := s.QuestionView(ctx, p, *q)
			if e != nil {
				return taskmodel.Detail{}, e
			}
			d.Nodes[i].Question = &view
		}
	}
	return d, nil
}
func (s *Service) QuestionView(ctx context.Context, p auth.Principal, q taskmodel.Question) (taskmodel.Question, error) {
	q.CanAnswer = false
	if q.Status != "open" || s.Source == nil || s.Source.ViewQuestion == nil {
		return q, nil
	}
	view, e := s.Source.ViewQuestion(ctx, p, q)
	if e != nil {
		return q, e
	}
	if view.ID != q.ID || view.ThreadID != q.ThreadID || view.MessageID != q.MessageID {
		return q, errors.New("统一问题引用不匹配")
	}
	switch view.Status {
	case "open":
	case "answered", "expired", "closed":
		view.CanAnswer = false
	default:
		return q, errors.New("统一问题状态无效")
	}
	return view, nil
}
