-- +goose Up
-- 对话页的 Pi 命令（plugins/SPEC.md §14）：消息以 /pi:<name> 开头时，发给 Pi 的是 /<name>，由 Pi 执行扩展命令、技能命令或提示模板。
-- pi_command 是这一轮执行的命令名（不带 "/"）；pi_command_handled 表示 Pi 直接处理了命令、没有交给模型（这一轮没有助手回复）。
ALTER TABLE runs ADD COLUMN pi_command TEXT;
ALTER TABLE runs ADD COLUMN pi_command_handled INTEGER NOT NULL DEFAULT 0 CHECK (pi_command_handled IN (0, 1));
