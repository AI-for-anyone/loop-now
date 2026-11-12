-- +goose Up
-- 本机模式第四个开关“用本机的登录状态”：Pi 的执行环境沿用服务端用户真实的 HOME。等于让 Agent 以用户本人的身份
-- 使用本机工具，新装与升级都默认关闭，须用户主动打开。
ALTER TABLE local_mode_settings ADD COLUMN logins INTEGER NOT NULL DEFAULT 0 CHECK (logins IN (0, 1));
