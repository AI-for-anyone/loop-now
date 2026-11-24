-- +goose Up
-- Local ACP commands people add: any program on this computer that speaks the Agent Client Protocol over stdio, run as
-- a runtime of its own (acp-<id>) with one model provider of the same id. command is a name on the login shell's PATH
-- or an absolute path; args is a JSON array of strings passed as is; env a JSON object of strings added for the program.
CREATE TABLE acp_commands (
    id          TEXT NOT NULL,
    name        TEXT NOT NULL,
    command     TEXT NOT NULL,
    args        TEXT NOT NULL CHECK (json_type(args) = 'array'),
    env         TEXT NOT NULL CHECK (json_type(env) = 'object'),
    created_at  INTEGER NOT NULL DEFAULT (now()),
    updated_at  INTEGER NOT NULL DEFAULT (now()),
    CONSTRAINT acp_commands_pkey PRIMARY KEY (id)
) STRICT;

-- +goose Down
DROP TABLE acp_commands;
