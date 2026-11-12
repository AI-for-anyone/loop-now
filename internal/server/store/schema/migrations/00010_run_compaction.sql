-- +goose Up
-- 手动压缩（/compact，origin = console_compact）的指令与结果，随执行记录保存：发起压缩的请求断开后照样执行、事后能读到结果。
-- {"instructions": string, "outcome": "compacted"|"skipped"|null, "skipReason": string|null,
--  "tokensBefore": int|null, "estimatedTokensAfter": int|null}；不是手动压缩时为空。迁移前的手动压缩也为空。
ALTER TABLE runs ADD COLUMN compaction TEXT CHECK (compaction IS NULL OR (origin = 'console_compact' AND json_type(compaction) = 'object'));

-- +goose Down
ALTER TABLE runs DROP COLUMN compaction;
