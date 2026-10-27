-- +goose Up
CREATE TABLE cli_credential_verifications (
 id uuid PRIMARY KEY CHECK(id<>'00000000-0000-0000-0000-000000000000'),
 project_id uuid NOT NULL,
 account_id uuid NOT NULL,
 version_id uuid NOT NULL CHECK(version_id<>'00000000-0000-0000-0000-000000000000'),
 -- Immutable version_id survives the owner's legitimate secret purge.
 live_version_id uuid,
 purpose text NOT NULL DEFAULT 'cli' CHECK(purpose='cli'),
 cli_name text NOT NULL,
 auth_method text NOT NULL CHECK(auth_method IN('static','service_account','oauth')),
 request_hash text NOT NULL CHECK(request_hash~'^[a-f0-9]{64}$'),
 initiator_kind text NOT NULL CHECK(initiator_kind IN('console','external')),
 initiator_id uuid,
 template_source text NOT NULL CHECK(length(template_source)>0),
 credential_format text NOT NULL CHECK(length(credential_format)>0),
 template_fingerprint text NOT NULL CHECK(template_fingerprint~'^[a-f0-9]{64}$'),
 tool_version text CHECK(length(tool_version)>0),
 adapter_revision text NOT NULL CHECK(length(adapter_revision)>0),
 probe_kind text NOT NULL CHECK(probe_kind IN('database_select1','github_user','google_crm_read','lark_bot')),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN('pending','running','succeeded','failed','cancelled','expired')),
 phase text DEFAULT 'preparing_tool' CHECK(phase IN('preparing_tool','resolving_credentials','probing','recording')),
 started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL,
 completed_at timestamptz,
 result_outcome text CHECK(result_outcome IN('passed','failed','cancelled','expired')),
 verified_at timestamptz,
 duration_ms bigint CHECK(duration_ms>=0),
 error_code text CHECK(error_code IN('tool_unsupported','tool_prepare_failed','credential_unavailable','configuration_invalid','tls_failed','authentication_failed','database_unavailable','permission_denied','provider_policy','rate_limited','network_failed','timeout','requires_login','target_changed','server_restarted','probe_failed','internal_error')),
 http_status integer CHECK(http_status BETWEEN 100 AND 599),
 FOREIGN KEY(account_id,project_id,purpose,cli_name) REFERENCES project_credential_accounts(id,project_id,purpose,slot_key),
 FOREIGN KEY(live_version_id,account_id,cli_name,auth_method) REFERENCES project_cli_versions(id,account_id,cli_name,auth_method) ON DELETE SET NULL (live_version_id),
 UNIQUE(id,version_id,account_id,cli_name,auth_method),
 CHECK(live_version_id IS NULL OR live_version_id=version_id),
 CHECK(expires_at>started_at),
 CHECK((initiator_kind='console' AND initiator_id IS NULL) OR (initiator_kind='external' AND initiator_id IS NOT NULL AND initiator_id<>'00000000-0000-0000-0000-000000000000')),
 CHECK(
  (status IN('pending','running') AND live_version_id IS NOT NULL AND phase IS NOT NULL AND completed_at IS NULL AND result_outcome IS NULL AND verified_at IS NULL AND duration_ms IS NULL AND error_code IS NULL AND http_status IS NULL)
  OR
  (status IN('succeeded','failed','cancelled','expired') AND phase IS NULL AND completed_at IS NOT NULL AND result_outcome IS NOT NULL AND duration_ms IS NOT NULL AND
   ((status='succeeded' AND result_outcome='passed' AND verified_at IS NOT NULL AND tool_version IS NOT NULL AND error_code IS NULL)
    OR (status='failed' AND result_outcome='failed' AND verified_at IS NULL AND error_code IS NOT NULL)
    OR (status='cancelled' AND result_outcome='cancelled' AND verified_at IS NULL AND error_code IS NULL)
    OR (status='expired' AND result_outcome='expired' AND verified_at IS NULL AND error_code='timeout')))
 )
);
CREATE UNIQUE INDEX cli_verification_active_account ON cli_credential_verifications(account_id) WHERE status IN('pending','running');
CREATE INDEX cli_verification_account_version ON cli_credential_verifications(account_id,version_id,started_at DESC,id DESC);
ALTER TABLE google_cli_grants ADD COLUMN lease_verification_id uuid;
ALTER TABLE google_cli_grants ADD CONSTRAINT google_cli_verification_lease_identity FOREIGN KEY(lease_verification_id,version_id,account_id,cli_name,auth_method) REFERENCES cli_credential_verifications(id,version_id,account_id,cli_name,auth_method);
ALTER TABLE google_cli_grants DROP CONSTRAINT google_cli_grants_check;
ALTER TABLE google_cli_grants ADD CONSTRAINT google_cli_refresh_lease_owner CHECK(
 (refresh_state='refreshing' AND lease_id IS NOT NULL AND lease_until IS NOT NULL AND num_nonnulls(lease_agent_id,lease_verification_id)=1)
 OR (refresh_state<>'refreshing' AND lease_id IS NULL AND lease_agent_id IS NULL AND lease_verification_id IS NULL AND lease_until IS NULL)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM cli_credential_verifications) OR EXISTS(SELECT 1 FROM google_cli_grants WHERE lease_verification_id IS NOT NULL) THEN
  RAISE EXCEPTION 'cli_verification_down_requires_no_new_data';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE google_cli_grants DROP CONSTRAINT google_cli_refresh_lease_owner,DROP CONSTRAINT google_cli_verification_lease_identity;
ALTER TABLE google_cli_grants ADD CONSTRAINT google_cli_grants_check CHECK((refresh_state='refreshing' AND lease_id IS NOT NULL AND lease_agent_id IS NOT NULL AND lease_until IS NOT NULL) OR (refresh_state<>'refreshing' AND lease_id IS NULL AND lease_agent_id IS NULL AND lease_until IS NULL));
ALTER TABLE google_cli_grants DROP COLUMN lease_verification_id;
DROP TABLE cli_credential_verifications;
