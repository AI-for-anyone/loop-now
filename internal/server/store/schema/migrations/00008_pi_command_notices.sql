-- +goose Up
-- Pi 直接处理的命令（plugins/SPEC.md §14）的输出：命令执行期间扩展用 ctx.ui.notify 给出的消息，随执行记录保存
-- （运行事件会按时间清理）。{"notices":[{"level","message"}],"truncated":bool}；不是被处理的命令时为空。
ALTER TABLE runs ADD COLUMN pi_command_notices TEXT CHECK (pi_command_notices IS NULL OR json_type(pi_command_notices) = 'object');
