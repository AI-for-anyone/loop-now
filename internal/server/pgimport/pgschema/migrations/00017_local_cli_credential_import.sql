-- +goose Up
CREATE TABLE cli_credential_import_operations (
 id uuid PRIMARY KEY CHECK(id<>'00000000-0000-0000-0000-000000000000'),
 project_id uuid NOT NULL REFERENCES projects(id),
 scan_id uuid NOT NULL CHECK(scan_id<>'00000000-0000-0000-0000-000000000000'),
 instance_id uuid NOT NULL CHECK(instance_id<>'00000000-0000-0000-0000-000000000000'),
 initiator_kind text NOT NULL DEFAULT 'console' CHECK(initiator_kind='console'),
 request_fingerprint bytea NOT NULL CHECK(octet_length(request_fingerprint)=32),
 expected_credentials_revision bigint NOT NULL CHECK(expected_credentials_revision>=1),
 provenance_key_id text NOT NULL CHECK(length(provenance_key_id) BETWEEN 1 AND 200),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN('pending','running','succeeded','failed','cancelled','expired')),
 phase text DEFAULT 'preparing' CHECK(phase IN('preparing','extracting','validating','committing')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 deadline_at timestamptz NOT NULL,
 completed_at timestamptz,
 error_code text,
 committed_credentials_revision bigint CHECK(committed_credentials_revision>=1),
 CHECK(deadline_at>created_at),
 CHECK((status IN('pending','running') AND phase IS NOT NULL AND completed_at IS NULL AND error_code IS NULL AND committed_credentials_revision IS NULL)
 OR (status IN('succeeded','failed','cancelled','expired') AND phase IS NULL AND completed_at IS NOT NULL AND
 ((status='succeeded' AND error_code IS NULL AND committed_credentials_revision IS NOT NULL)
 OR (status<>'succeeded' AND error_code IS NOT NULL AND committed_credentials_revision IS NULL)))),
 CHECK(error_code IS NULL OR error_code IN('source_changed','source_unreadable','missing_field','unsupported','template_mismatch','supplement_override','invalid_credential','consumer_failed','precondition_failed','source_conflict','provenance_key_changed','cancelled','timeout','worker_restarted','project_deleted','internal_error'))
);
CREATE UNIQUE INDEX cli_import_active_project ON cli_credential_import_operations(project_id) WHERE status IN('pending','running');
CREATE TABLE cli_credential_import_items (
 operation_id uuid NOT NULL REFERENCES cli_credential_import_operations(id),
 candidate_id uuid NOT NULL CHECK(candidate_id<>'00000000-0000-0000-0000-000000000000'),
 ordinal integer NOT NULL CHECK(ordinal>=0 AND ordinal<20),
 cli_name text NOT NULL CHECK(cli_name IN('gh','lark-cli','gcloud','psql','mysql')),
 label text NOT NULL CHECK(length(label)<=200),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN('pending','not_committed','imported','already_imported','failed')),
 account_id uuid CHECK(account_id<>'00000000-0000-0000-0000-000000000000'),
 version_id uuid CHECK(version_id<>'00000000-0000-0000-0000-000000000000'),
 error_code text,
 PRIMARY KEY(operation_id,candidate_id), UNIQUE(operation_id,ordinal),
 CHECK((status IN('imported','already_imported') AND account_id IS NOT NULL AND version_id IS NOT NULL AND error_code IS NULL)
 OR (status NOT IN('imported','already_imported') AND account_id IS NULL AND version_id IS NULL)),
 CHECK((status='failed')=(error_code IS NOT NULL)),
 CHECK(error_code IS NULL OR error_code IN('source_changed','source_unreadable','missing_field','unsupported','template_mismatch','supplement_override','invalid_credential','consumer_failed','precondition_failed','source_conflict','provenance_key_changed','cancelled','timeout','worker_restarted','project_deleted','internal_error'))
);
CREATE TABLE cli_credential_import_receipts (
 project_id uuid NOT NULL REFERENCES projects(id),
 provenance_key_id text NOT NULL CHECK(length(provenance_key_id) BETWEEN 1 AND 200),
 origin_mac bytea NOT NULL CHECK(octet_length(origin_mac)=32),
 content_mac bytea NOT NULL CHECK(octet_length(content_mac)=32),
 purpose text NOT NULL DEFAULT 'cli' CHECK(purpose='cli'),
 cli_name text NOT NULL CHECK(cli_name IN('gh','lark-cli','gcloud','psql','mysql')),
 credential_format text NOT NULL CHECK(length(credential_format)>0),
 account_id uuid NOT NULL CHECK(account_id<>'00000000-0000-0000-0000-000000000000'),
 version_id uuid NOT NULL CHECK(version_id<>'00000000-0000-0000-0000-000000000000'),
 live_account_id uuid,
 live_version_id uuid,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(project_id,provenance_key_id,origin_mac),
 CHECK(live_account_id IS NULL OR live_account_id=account_id),
 CHECK(live_version_id IS NULL OR live_version_id=version_id),
 FOREIGN KEY(live_account_id,project_id,purpose,cli_name) REFERENCES project_credential_accounts(id,project_id,purpose,slot_key) ON DELETE SET NULL (live_account_id),
 FOREIGN KEY(live_version_id,account_id) REFERENCES project_cli_versions(id,account_id) ON DELETE SET NULL (live_version_id)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM cli_credential_import_operations) OR EXISTS(SELECT 1 FROM cli_credential_import_items) OR EXISTS(SELECT 1 FROM cli_credential_import_receipts) THEN
  RAISE EXCEPTION 'cli_import_down_requires_no_history';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE cli_credential_import_receipts;
DROP TABLE cli_credential_import_items;
DROP TABLE cli_credential_import_operations;
