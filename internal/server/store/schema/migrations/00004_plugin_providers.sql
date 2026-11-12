-- +goose Up
-- 插件注册的模型提供方（plugins/SPEC.md §13）：安装或更新确认后，用随包 Pi 加载插件的 Pi 扩展，
-- 读出它注册的提供方与模型，随版本保存；providers_error 是整次读取失败的原因（读取失败不阻止安装）。
ALTER TABLE plugin_versions ADD COLUMN providers TEXT NOT NULL DEFAULT '{"providers":[],"errors":[]}' CHECK (json_type(providers) = 'object');
ALTER TABLE plugin_versions ADD COLUMN providers_error TEXT;
