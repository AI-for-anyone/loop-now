package store

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/messageimage"
	"sort"
	"strings"
)

type ImageOwner struct{ AgentID, TaskID, NodeRunID, RuntimeID uuid.UUID }
type StoredImage struct {
	messageimage.Ref
	ImageOwner
	BlobSHA                 string
	ConversationID, InboxID *uuid.UUID
}

func scanImage(row pgx.Row) (StoredImage, error) {
	var v StoredImage
	var task, node, runtime *uuid.UUID
	e := row.Scan(&v.ID, &v.AgentID, &task, &node, &runtime, &v.BlobSHA, &v.MimeType, &v.Size, &v.Width, &v.Height, &v.ConversationID, &v.InboxID)
	if task != nil {
		v.TaskID = *task
		v.NodeRunID = *node
		v.RuntimeID = *runtime
	}
	return v, notFoundIfNoRows(e, "图片")
}

const imageColumns = `id,agent_id,task_id,node_run_id,runtime_id,blob_sha,mime_type,size,width,height,conversation_message_id,inbox_message_id`

func (s *Store) MessageImage(ctx context.Context, id uuid.UUID) (StoredImage, error) {
	return scanImage(s.Pool.QueryRow(ctx, `SELECT `+imageColumns+` FROM message_images WHERE id=$1`, id))
}
func (s *Store) SaveMessageImage(ctx context.Context, id uuid.UUID, owner ImageOwner, ref messageimage.Ref, data []byte) (messageimage.Ref, error) {
	var result messageimage.Ref
	e := s.inTx(ctx, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `SELECT id FROM agents WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, owner.AgentID).Scan(new(uuid.UUID)); e != nil {
			return notFoundIfNoRows(e, "Agent")
		}
		sha, e := putBlob(ctx, tx, data)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO message_images(id,agent_id,task_id,node_run_id,runtime_id,blob_sha,mime_type,size,width,height) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(id) DO NOTHING`, id, owner.AgentID, nullableUUID(owner.TaskID), nullableUUID(owner.NodeRunID), nullableUUID(owner.RuntimeID), sha, ref.MimeType, ref.Size, ref.Width, ref.Height)
		if e != nil {
			return e
		}
		v, e := scanImage(tx.QueryRow(ctx, `SELECT `+imageColumns+` FROM message_images WHERE id=$1`, id))
		if e != nil {
			return e
		}
		if v.ImageOwner != owner || v.BlobSHA != sha {
			return apperr.Conflict(apperr.CodeTaskConflict, "图片ID已被其它内容或作用域使用")
		}
		result = v.Ref
		return nil
	})
	return result, e
}
func nullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// Bind uses the same transaction as the message and queue. Reusing an image for
// another message, Agent, node, or runtime is refused, never silently copied.
func (s *Store) BindMessageImagesTx(ctx context.Context, tx pgx.Tx, owner ImageOwner, message uuid.UUID, ids []uuid.UUID, maxCount int) ([]messageimage.Ref, error) {
	out, e := s.lockMessageImagesTx(ctx, tx, owner, ids, maxCount)
	if e != nil {
		return nil, e
	}
	column := "conversation_message_id"
	if owner.TaskID != uuid.Nil {
		column = "inbox_message_id"
	}
	for _, v := range out {
		if _, e = tx.Exec(ctx, `UPDATE message_images SET `+column+`=$2 WHERE id=$1`, v.ID, message); e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (s *Store) lockMessageImagesTx(ctx context.Context, tx pgx.Tx, owner ImageOwner, ids []uuid.UUID, maxCount int) ([]messageimage.Ref, error) {
	if maxCount <= 0 {
		maxCount = messageimage.DefaultCount
	}
	if len(ids) > maxCount {
		return nil, apperr.TooLarge("图片数量超限")
	}
	seen := map[uuid.UUID]bool{}
	ordered := append([]uuid.UUID(nil), ids...)
	for _, id := range ids {
		if id == uuid.Nil || seen[id] {
			return nil, apperr.Invalid("imageIds", "图片ID为空或重复")
		}
		seen[id] = true
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].String() < ordered[j].String() })
	byID := map[uuid.UUID]messageimage.Ref{}
	for _, id := range ordered {
		v, e := scanImage(tx.QueryRow(ctx, `SELECT `+imageColumns+` FROM message_images WHERE id=$1 FOR UPDATE`, id))
		if e != nil {
			return nil, e
		}
		if v.ImageOwner != owner {
			return nil, apperr.NotFound("图片")
		}
		if v.ConversationID != nil || v.InboxID != nil {
			return nil, apperr.Conflict(apperr.CodeTaskConflict, "图片已属于另一条消息")
		}
		byID[id] = v.Ref
	}
	out := []messageimage.Ref{}
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out, nil
}

func (s *Store) ImageContents(ctx context.Context, refs []messageimage.Ref) ([]messageimage.Content, error) {
	out := []messageimage.Content{}
	for _, r := range refs {
		v, e := s.MessageImage(ctx, r.ID)
		if e != nil {
			return nil, e
		}
		data, e := s.ReadBlob(ctx, v.BlobSHA)
		if e != nil {
			return nil, e
		}
		out = append(out, messageimage.Encode(v.MimeType, data))
	}
	return out, nil
}

// Strict reminder ownership is the source of visual association. This does not
// mark Inbox read, attach another runtime's unread items, or pick the latest run.
func (s *Store) TaskTurnImageInput(ctx context.Context, t TaskTurn) ([]messageimage.Content, string, error) {
	rows, e := s.Pool.Query(ctx, `SELECT m.id,m.images FROM task_node_inbox_messages b JOIN inbox_messages m ON m.id=b.message_id WHERE b.reminder_run_id=$1 AND b.task_id=$2 AND b.runtime_id=$3 AND b.node_run_id=$4 AND b.not_executed_at IS NULL ORDER BY m.created_at,m.id`, t.ID, t.TaskID, t.RuntimeID, t.NodeRunID)
	if e != nil {
		return nil, "", e
	}
	defer rows.Close()
	var refs []messageimage.Ref
	var notes []string
	for rows.Next() {
		var id uuid.UUID
		var raw []byte
		if e = rows.Scan(&id, &raw); e != nil {
			return nil, "", e
		}
		var images []messageimage.Ref
		if e = json.Unmarshal(raw, &images); e != nil {
			return nil, "", e
		}
		if len(images) > 0 {
			notes = append(notes, fmt.Sprintf("图片%d-%d来自Inbox消息%s；提供图片不代表消息已读，仍需按原规则inbox read。", len(refs)+1, len(refs)+len(images), id))
			refs = append(refs, images...)
		}
	}
	if e = rows.Err(); e != nil {
		return nil, "", e
	}
	rows.Close()
	images, e := s.ImageContents(ctx, refs)
	return images, strings.Join(notes, "\n"), e
}
