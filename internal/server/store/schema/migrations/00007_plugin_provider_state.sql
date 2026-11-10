-- +goose Up
-- 插件提供方哪些状态随项目账号走（plugins/SPEC.md §13）：安装时从市场条目的 providerState（优先）或包自己的
-- loopNow.providerState 抄下，随版本保存；{ "<providerId>": { "paths": [HOME 下的相对路径] } }。
ALTER TABLE plugin_versions ADD COLUMN provider_state TEXT NOT NULL DEFAULT '{}' CHECK (json_type(provider_state) = 'object');
