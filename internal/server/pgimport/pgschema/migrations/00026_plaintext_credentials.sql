-- +goose Up
-- Credentials are stored as plaintext. Rows written under the removed master
-- key must first pass the one-time plaintext conversion, which marks each
-- location in a form this migration recognizes. Any leftover encrypted record
-- refuses the whole migration; nothing is silently dropped or rewritten.
-- +goose StatementBegin
DO $$
DECLARE
 n bigint;
 problems text := '';
BEGIN
 SELECT count(*) INTO n FROM agent_credentials WHERE alg<>'plaintext-v1';
 IF n>0 THEN problems := problems || format(' agent_credentials=%s', n); END IF;
 SELECT count(*) INTO n FROM model_auth_versions WHERE alg<>'plaintext-v1';
 IF n>0 THEN problems := problems || format(' model_auth_versions=%s', n); END IF;
 SELECT count(*) INTO n FROM project_cli_versions WHERE alg<>'plaintext-v1';
 IF n>0 THEN problems := problems || format(' project_cli_versions=%s', n); END IF;
 SELECT count(*) INTO n FROM project_credential_legacy_rows WHERE alg<>'plaintext-v1';
 IF n>0 THEN problems := problems || format(' project_credential_legacy_rows=%s', n); END IF;
 SELECT count(*) INTO n FROM plugin_instances WHERE alg IS NOT NULL OR key_id IS NOT NULL OR wrapped_dek IS NOT NULL OR nonce IS NOT NULL OR ciphertext IS NOT NULL;
 IF n>0 THEN problems := problems || format(' plugin_instances=%s', n); END IF;
 SELECT count(*) INTO n FROM platform_plugin_account_versions
  WHERE cipher<>'{}'::jsonb AND NOT (cipher->>'format'='plaintext-v1' AND jsonb_typeof(cipher->'values')='object' AND (SELECT count(*) FROM jsonb_object_keys(cipher))=2);
 IF n>0 THEN problems := problems || format(' platform_plugin_account_versions=%s', n); END IF;
 SELECT count(*) INTO n FROM project_platform_plugins p WHERE EXISTS(SELECT 1 FROM jsonb_each(p.secrets) e WHERE jsonb_typeof(e.value)<>'string');
 IF n>0 THEN problems := problems || format(' project_platform_plugins=%s', n); END IF;
 SELECT count(*) INTO n FROM platform_plugin_config_versions p WHERE EXISTS(SELECT 1 FROM jsonb_each(p.secrets) e WHERE jsonb_typeof(e.value)<>'string');
 IF n>0 THEN problems := problems || format(' platform_plugin_config_versions=%s', n); END IF;
 SELECT count(*) INTO n FROM task_instances i WHERE CASE
   WHEN NOT pg_input_is_valid(convert_from(i.snapshot,'UTF8'),'jsonb') THEN true
   ELSE coalesce(convert_from(i.snapshot,'UTF8')::jsonb->>'schemaVersion','')<>'8'
     OR coalesce(jsonb_typeof(convert_from(i.snapshot,'UTF8')::jsonb->'credentials'),'')<>'object'
     OR convert_from(i.snapshot,'UTF8')::jsonb->'credentials' ? 'Ciphertext'
   END;
 IF n>0 THEN problems := problems || format(' task_instances=%s', n); END IF;
 IF EXISTS(SELECT 1 FROM task_instances) THEN
  IF to_regclass('plaintext_credential_snapshot_shas') IS NULL THEN
   problems := problems || ' task_instances.original_snapshot_sha=missing';
  ELSE
   EXECUTE 'SELECT count(*) FROM task_instances i WHERE NOT EXISTS(SELECT 1 FROM plaintext_credential_snapshot_shas h WHERE h.runtime_id=i.id AND h.original_sha ~ ''^[0-9a-f]{64}$'')' INTO n;
   IF n>0 THEN problems := problems || format(' task_instances.original_snapshot_sha=%s', n); END IF;
  END IF;
 END IF;
 IF problems<>'' THEN
  RAISE EXCEPTION '仍有未转换的加密凭证记录:%。请先停止服务，用原主密钥运行一次性明文凭证转换程序，再启动本版本', problems;
 END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE agent_credentials DROP COLUMN alg, DROP COLUMN key_id, DROP COLUMN wrapped_dek, DROP COLUMN nonce;
ALTER TABLE agent_credentials RENAME COLUMN ciphertext TO secret;
ALTER TABLE model_auth_versions DROP COLUMN alg, DROP COLUMN key_id, DROP COLUMN wrapped_dek, DROP COLUMN nonce;
ALTER TABLE model_auth_versions RENAME COLUMN ciphertext TO secret;
ALTER TABLE project_cli_versions DROP COLUMN alg, DROP COLUMN key_id, DROP COLUMN wrapped_dek, DROP COLUMN nonce;
ALTER TABLE project_cli_versions RENAME COLUMN ciphertext TO secret;
ALTER TABLE project_credential_legacy_rows DROP COLUMN alg, DROP COLUMN key_id, DROP COLUMN wrapped_dek, DROP COLUMN nonce;
ALTER TABLE project_credential_legacy_rows RENAME COLUMN ciphertext TO secret;
ALTER TABLE plugin_instances DROP CONSTRAINT plugin_instances_sealed_all_or_none,
 DROP COLUMN alg, DROP COLUMN key_id, DROP COLUMN wrapped_dek, DROP COLUMN nonce, DROP COLUMN ciphertext;

-- All account values (public and secret) in plaintext; NULL once purged.
ALTER TABLE platform_plugin_account_versions RENAME COLUMN cipher TO secret;
ALTER TABLE platform_plugin_account_versions ALTER COLUMN secret DROP NOT NULL;
UPDATE platform_plugin_account_versions SET secret=CASE WHEN secret='{}'::jsonb THEN NULL ELSE secret->'values' END;

-- Frozen identity of the originally captured snapshot. Lark switch bindings,
-- receipts and request hashes pin this value, never a hash of current bytes.
ALTER TABLE task_instances ADD COLUMN snapshot_sha text;
-- +goose StatementBegin
DO $$
BEGIN
 IF to_regclass('plaintext_credential_snapshot_shas') IS NOT NULL THEN
  EXECUTE 'UPDATE task_instances i SET snapshot_sha=h.original_sha FROM plaintext_credential_snapshot_shas h WHERE h.runtime_id=i.id';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE task_instances ALTER COLUMN snapshot_sha SET NOT NULL,
 ADD CONSTRAINT task_instances_snapshot_sha CHECK(snapshot_sha ~ '^[0-9a-f]{64}$');
DROP TABLE IF EXISTS plaintext_credential_snapshot_shas;

-- +goose Down
-- Plaintext cannot be re-encrypted: refuse once any credential data exists.
-- An empty schema rolls back to the 025 layout.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM agent_credentials) OR EXISTS(SELECT 1 FROM model_auth_versions)
 OR EXISTS(SELECT 1 FROM project_cli_versions) OR EXISTS(SELECT 1 FROM project_credential_legacy_rows)
 OR EXISTS(SELECT 1 FROM platform_plugin_account_versions)
 OR EXISTS(SELECT 1 FROM project_platform_plugins WHERE secrets<>'{}'::jsonb)
 OR EXISTS(SELECT 1 FROM platform_plugin_config_versions WHERE secrets<>'{}'::jsonb)
 OR EXISTS(SELECT 1 FROM task_instances) THEN
  RAISE EXCEPTION 'plaintext_credentials_down_refused: plaintext credentials cannot be re-encrypted';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE task_instances DROP COLUMN snapshot_sha;
ALTER TABLE platform_plugin_account_versions RENAME COLUMN secret TO cipher;
ALTER TABLE platform_plugin_account_versions ALTER COLUMN cipher SET NOT NULL;
ALTER TABLE plugin_instances ADD COLUMN alg text, ADD COLUMN key_id text, ADD COLUMN wrapped_dek bytea, ADD COLUMN nonce bytea, ADD COLUMN ciphertext bytea,
 ADD CONSTRAINT plugin_instances_sealed_all_or_none CHECK(
  (alg IS NULL AND key_id IS NULL AND wrapped_dek IS NULL AND nonce IS NULL AND ciphertext IS NULL)
  OR (alg IS NOT NULL AND key_id IS NOT NULL AND wrapped_dek IS NOT NULL AND nonce IS NOT NULL AND ciphertext IS NOT NULL));
ALTER TABLE project_credential_legacy_rows RENAME COLUMN secret TO ciphertext;
ALTER TABLE project_credential_legacy_rows ADD COLUMN alg text NOT NULL, ADD COLUMN key_id text NOT NULL, ADD COLUMN wrapped_dek bytea NOT NULL, ADD COLUMN nonce bytea NOT NULL;
ALTER TABLE project_cli_versions RENAME COLUMN secret TO ciphertext;
ALTER TABLE project_cli_versions ADD COLUMN alg text NOT NULL, ADD COLUMN key_id text NOT NULL, ADD COLUMN wrapped_dek bytea NOT NULL, ADD COLUMN nonce bytea NOT NULL;
ALTER TABLE model_auth_versions RENAME COLUMN secret TO ciphertext;
ALTER TABLE model_auth_versions ADD COLUMN alg text NOT NULL, ADD COLUMN key_id text NOT NULL, ADD COLUMN wrapped_dek bytea NOT NULL, ADD COLUMN nonce bytea NOT NULL;
ALTER TABLE agent_credentials RENAME COLUMN secret TO ciphertext;
ALTER TABLE agent_credentials ADD COLUMN alg text NOT NULL, ADD COLUMN key_id text NOT NULL, ADD COLUMN wrapped_dek bytea NOT NULL, ADD COLUMN nonce bytea NOT NULL;
