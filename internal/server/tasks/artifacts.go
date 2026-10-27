package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/content"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
)

func (s *Service) Publish(ctx context.Context, p auth.Principal, taskID, nodeID, actionID uuid.UUID, name, contentType string, data []byte) (json.RawMessage, error) {
	if name == "" || name == "." || name == ".." || name != path.Base(name) || strings.ContainsAny(name, "/\\") || len([]rune(name)) > 200 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return nil, apperr.Invalid("name", "成果名称必须是单个有效文件名")
	}
	if int64(len(data)) > s.Options.MaxArtifact {
		return nil, apperr.TooLarge("成果超限")
	}
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.Contains(media, "/") {
		return nil, apperr.Invalid("contentType", "成果媒体类型无效")
	}
	sha := content.SHA256Hex(data)
	action := bodyAction(p, actionID, "publishTaskArtifact", struct {
		NodeID                    uuid.UUID
		Name, ContentType, SHA256 string
	}{nodeID, name, contentType, sha})
	action.Authorize = func(m *store.TaskMutation) error { return historyActor(p, m, nodeID) }
	return s.Store.MutateTask(ctx, taskID, action, func(m *store.TaskMutation) (any, error) {
		if e := nodeActor(p, m, nodeID); e != nil {
			return nil, e
		}
		if m.Record.Ended() || m.Record.Current().Status == "blocked" {
			return nil, conflict("当前节点不能发布成果")
		}
		_, e := m.Tx.Exec(ctx, `INSERT INTO blobs(sha256,size,data) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, sha, len(data), data)
		if e != nil {
			return nil, e
		}
		a := taskmodel.Artifact{ID: uuid.Must(uuid.NewV7()), TaskID: taskID, NodeRunID: nodeID, Name: name, ContentType: contentType, Size: int64(len(data)), SHA256: sha, CreatedAt: time.Now().UTC()}
		a.DownloadPath = fmt.Sprintf("/tasks/%s/artifacts/%s", taskID, a.ID)
		_, e = m.Tx.Exec(ctx, `INSERT INTO task_artifacts(id,task_id,node_run_id,name,content_type,blob_sha256,size,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, a.ID, a.TaskID, a.NodeRunID, a.Name, a.ContentType, a.SHA256, a.Size, a.CreatedAt)
		if e != nil {
			return nil, e
		}
		m.Record.View.Artifacts = append(m.Record.View.Artifacts, a)
		if e = m.Emit("artifact.published", map[string]any{"artifact": a}); e != nil {
			return nil, e
		}
		return a, nil
	})
}
func (s *Service) Download(ctx context.Context, p auth.Principal, taskID, artifactID uuid.UUID) (taskmodel.Artifact, []byte, error) {
	detail, err := s.Get(ctx, p, taskID)
	if err != nil {
		return taskmodel.Artifact{}, nil, err
	}
	for _, a := range detail.Artifacts {
		if a.ID == artifactID {
			data, e := s.Store.ReadBlob(ctx, a.SHA256)
			return a, data, e
		}
	}
	return taskmodel.Artifact{}, nil, apperr.NotFound("成果")
}
