-- +goose Up
-- A deleted project account whose versions an unfinished task still uses
-- (a snapshot binding, a Lark task binding or a captured plaintext copy).
-- Those versions keep their secret until the last such task ends. Every task
-- save in the project and every server start settles the account again; the
-- row goes away once nothing of the account is left to wipe.
CREATE TABLE credential_pending_wipes (
 account_id uuid PRIMARY KEY,
 project_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(account_id,project_id) REFERENCES project_credential_accounts(id,project_id)
);
CREATE INDEX credential_pending_wipes_project ON credential_pending_wipes(project_id);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM credential_pending_wipes) THEN
  RAISE EXCEPTION 'credential_pending_wipes_down_refused: deleted accounts still wait for their secrets to be wiped';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE credential_pending_wipes;
