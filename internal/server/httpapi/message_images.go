package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/messageimage"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskruntime"
	"mime"
	"net/http"
)

func (s *Server) imageIDs(raw json.RawMessage) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	if raw == nil {
		return ids, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &ids) != nil {
		return nil, apperr.Invalid("imageIds", "须为图片ID数组")
	}
	if len(ids) > s.O.Limits.MaxMessageImages {
		return nil, apperr.TooLarge("图片数量超限")
	}
	return ids, nil
}
func (s *Server) imageModel(ctx context.Context, owner store.ImageOwner) error {
	var provider, model string
	if owner.TaskID == uuid.Nil {
		a, e := s.Store.GetAgent(ctx, owner.AgentID)
		if e != nil {
			return e
		}
		provider, model = a.ModelProvider, a.ModelID
	} else {
		inst, e := s.Store.TaskInstance(ctx, owner.RuntimeID)
		if e != nil {
			return e
		}
		if inst.TaskID != owner.TaskID || inst.AgentID != owner.AgentID {
			return apperr.NotFound("任务实例")
		}
		snap, e := taskruntime.Decode(inst.Snapshot)
		if e != nil {
			return apperr.Invalid("images", "任务配置不可用")
		}
		provider, model = snap.Configuration().Provider, snap.Configuration().ModelID
	}
	if !s.Models.SupportsImages(provider, model) {
		return apperr.Invalid("imageIds", "选定模型不支持图片，未发送消息；请明确选择支持图片的模型")
	}
	return nil
}
func (s *Server) uploadMessageImage(w http.ResponseWriter, r *http.Request) {
	id, e := pathUUID(r, "imageId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if id == uuid.Nil {
		s.fail(w, r, apperr.Invalid("imageId", "图片ID不得为空"))
		return
	}
	owner := store.ImageOwner{}
	if r.PathValue("taskId") != "" {
		task, e := pathUUID(r, "taskId")
		if e != nil {
			s.fail(w, r, e)
			return
		}
		node, e := pathUUID(r, "nodeRunId")
		if e != nil {
			s.fail(w, r, e)
			return
		}
		if !s.taskReady(w, r) {
			return
		}
		cap, e := s.Tasks.ConsoleMessageCapability(r.Context(), principal(r), task, node)
		if e != nil {
			s.fail(w, r, e)
			return
		}
		if !cap.CanSend {
			s.fail(w, r, apperr.Conflict(apperr.CodeTaskConflict, "原节点当前不能接收图片；保留原草稿"))
			return
		}
		owner = store.ImageOwner{AgentID: cap.AgentID, TaskID: task, NodeRunID: node, RuntimeID: cap.RuntimeID}
	} else {
		owner.AgentID, e = pathUUID(r, "agentId")
		if e != nil {
			s.fail(w, r, e)
			return
		}
	}
	if e = s.imageModel(r.Context(), owner); e != nil {
		s.fail(w, r, e)
		return
	}
	mimeType, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil {
		s.fail(w, r, apperr.Invalid("Content-Type", "图片类型无效"))
		return
	}
	data, e := io.ReadAll(http.MaxBytesReader(w, r.Body, s.O.Limits.MaxMessageImageBytes))
	if e != nil {
		s.fail(w, r, apperr.TooLarge("图片字节超限"))
		return
	}
	ref, e := messageimage.Inspect(data, mimeType, s.O.Limits.MaxMessageImageBytes, s.O.Limits.MaxMessageImagePixels)
	if e != nil {
		s.fail(w, r, apperr.Invalid("image", e.Error()))
		return
	}
	ref, e = s.Store.SaveMessageImage(r.Context(), id, owner, ref, data)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 201, ref)
}
func (s *Server) getMessageImage(w http.ResponseWriter, r *http.Request) {
	id, e := pathUUID(r, "imageId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	v, e := s.Store.MessageImage(r.Context(), id)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if v.ConversationID == nil && v.InboxID == nil {
		s.fail(w, r, apperr.NotFound("图片"))
		return
	}
	if v.TaskID == uuid.Nil {
		if _, e = s.Store.GetAgent(r.Context(), v.AgentID); e != nil {
			s.fail(w, r, e)
			return
		}
	} else {
		task, e := s.Store.GetTask(r.Context(), v.TaskID)
		if e != nil {
			s.fail(w, r, e)
			return
		}
		if _, e = s.Store.GetProject(r.Context(), task.View.Task.ProjectID); e != nil {
			s.fail(w, r, e)
			return
		}
		found := false
		for _, n := range task.View.Nodes {
			if n.ID == v.NodeRunID && n.RuntimeID == v.RuntimeID && n.AgentID == v.AgentID {
				found = true
			}
		}
		if !found {
			s.fail(w, r, apperr.NotFound("图片"))
			return
		}
	}
	data, e := s.Store.ReadBlob(r.Context(), v.BlobSHA)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	w.Header().Set("Content-Type", v.MimeType)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(200)
	_, _ = w.Write(data)
}
