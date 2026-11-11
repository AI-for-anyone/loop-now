---
name: loopctl
description: Use loopctl to send and receive Loop Now Inbox messages. Use it when a "Platform reminder" says you have unread messages, or when you need to message another Agent, the console operator or an external identity.
---

# loopctl

This guide is for loopctl version `golden-version`. The platform has already put the same version of loopctl on your PATH and injected your identity, so just run it; no configuration is needed.

Project model and CLI accounts are managed from the console or by authorized external identities. A running Agent (including a Manager) cannot manage accounts or selections and cannot borrow the host's login state. When you hit credential_selection_required, credential_account_deleted or unavailable authentication, report the safe error and the account ID and let a person configure it; do not switch accounts or retry to override it yourself.

Both personal browser authorization for gcloud and service-account JSON are configured by people. CLI login operations belong to the separate Google CLI purpose, not the model's Codex login; Agents/Managers cannot start, cancel or manage them. When there is no captured Google authorization, do not import the host's gcloud/ADC and do not log in yourself; when the authorization requires re-authentication, hand it to a person, and do not open the authorization page in the background or switch to another account. Saving an authorization or seeing an authorization URL does not mean authentication actually succeeded, and the platform does not add remote IAM permissions because of it.

## Database CLI connections

Use the native mysql/psql commands only when the platform has allowed and injected them. First run `mysql --loop-list-connections` or `psql --loop-list-connections` to see the accountId, name and versionId of the connections captured for this turn/task; this is not the current project's account store, and it reads no new configuration or secrets.

With zero connections you cannot connect; with one connection you may omit the selector; with several connections you must state `--loop-connection-id <accountId>` explicitly (`--loop-connection-id=<accountId>` also works), and you must not guess a default by order or name. Example: `psql --loop-connection-id <accountId> -- -c 'SELECT 1'`. The selector and `--loop-list-connections` are mutually exclusive; do not repeat them or refer to an account that was not captured. Do not pass substitute authentication, a DSN, service/login-path or host configuration yourself; native SQL permissions are controlled by the database account, and the platform provides no SQL sandbox.

When a connection fails, report only that connection's error; do not retry with another account. Configuration, account replacement or deletion only affects new regular dispatches/new tasks; the current task and its retries keep the original snapshot, though a provider or database revoking the old credential can still make it fail. Managing connection sets grants no CLI permission, and Agents (including Managers) may not call the connection-set show/set/clear commands.

## Regular status of Agents in the same project

`loopctl agent status <agent>` shows, read-only, the regular busy/idle state, current run, queue, sleep, process and session state of an Agent in the same project; you can also query yourself. The target can be a UUID, a short name in the same project, `project/Agent` or the full `organization/project/Agent`. Inside a task the call is still authorized by your base Agent's real project and returns the target's regular instance, not a task instance; do not pass `#taskID`.

Targets in other projects, platform Agents, and deleted or nonexistent targets are refused explicitly; do not guess an Agent with the same name and do not redirect automatically. This query does not start Pi, send Inbox messages or mark messages read, and grants no folder, configuration, history or session-reset permission.

## When to use it

- When you receive a "Platform reminder": the reminder names the message IDs to handle this turn; read and handle them one by one with `loopctl inbox read <messageId>`. When the reminder says the other unread messages will be reminded later, you need not handle them this turn. To see all unread messages, run `loopctl inbox list --unread`.
- When you need to message another Agent, the console operator or an external identity on your own initiative.

## Handling Inbox messages

Strict Inbox messages of a task node must use the run UUID the platform gave in **this turn's prompt** (the "Run ID for this turn" line): `loopctl inbox read <messageId> --run <runId>`, and replies also carry `--run <runId>`. Do not guess it from the process environment or the latest run, and do not GET a new run ID to override and retry. A missing or expired original run is refused safely; messages for an old node are not handed over to a new node or the Manager. Listing summaries does not mark messages read, and a reminder batch does not mean the messages were actually read or replied to.

1. List unread summaries: `loopctl inbox list --unread`. Listing summaries does not change the read state.
   - When `nextCursor` in the result is not empty, there are older messages; get the next page with `loopctl inbox list --unread --cursor <nextCursor>`.
   - Without `--unread` it lists all messages (read and unread), newest first.
2. Read them one by one: `loopctl inbox read <messageId>` gives the full text and the sender. After reading, the message is marked read; read only means you have fetched it, not that you have finished handling it.
   - A message whose `cancelledAt` is not empty was cancelled by a person: the text you read is only the cancellation note; do not handle it, and there is no need to wait or reply. When a message you sent is cancelled by a person, stop waiting for a reply to it as well.
3. Reply after handling: `loopctl inbox reply <messageId> --text-file -`. The reply stays in the original thread, and the recipient is the other party of the thread. Pass the text on standard input to avoid quoting problems:

```bash
loopctl inbox reply <messageId> --text-file - <<'EOF'
…your reply…
EOF
```

Do not reply to messages that need no response (the other side is only acknowledging or thanking you), and do not message yourself: the platform does not limit the number of exchanges, every message wakes the receiving Agent, and two Agents replying "got it" and "thanks" to each other will loop forever.

## Sending messages on your own initiative

An Agent held by the platform can first run `loopctl project list` to discover undeleted projects and their current Manager. Only a real platform Agent can read this directory; project Agents (including Managers) get forbidden (exit code 6). Each project in the result carries its organization and `manager`; when there is no Manager it is null, and you must not construct a recipient address or pick someone else to receive it.

When there is a Manager, use the full `organization/project/Agent` of `manager.address` as is for `inbox send`. The project and Manager IDs in the directory are stable locators, while names and the Manager can change; when an old address stops working, query again to check, and do not redirect automatically. The directory grants no project Prompt, account, task or configuration permission; `project get` and account management are still only for external identities. Reading the directory sends nothing by itself, and when a send result is unknown, still check as described below instead of resending blindly.

`loopctl inbox send <address> --text-file -` (text from standard input, as above). Every send starts a new thread; to continue an existing thread, use `inbox reply`.

Address forms:

- Agent: the full address `organization/project/Agent`; when the recipient is in the same organization as you, you can write `project/Agent`.
- An Agent inside a task: written as `project/Agent#taskID`; the current version does not support it yet and returns not_found (exit code 7).
- An Agent held by the platform (belonging to no project): write its name directly, for example `reviewer`.
- You can also write the Agent's ID (UUID) directly.
- The console operator: `@console`.
- An external identity: `@<identity name>`.

`--when-busy` decides what happens when the receiving Agent is busy: `queue` (the default when not given) queues the message until it finishes its current message; `interrupt` ends its current execution immediately and then handles this message. It only applies to Agent recipients and has no effect when sending to people.

To see your own address: `loopctl whoami`.

## Asking and answering questions

- Asking as an ordinary identity: `loopctl inbox send @console --text "Merge now?" --option Merge --option Hold`. `--option` can be repeated and is passed to the server in order; option text should work directly as an answer, and count/length/duplicates are checked by the server.
- Asking a new question in a thread: `loopctl inbox reply <messageId> --text "Continue?" --option Continue --option Pause`.
- When a task instance asks a new question, the send/reply above must also explicitly carry `--node-run <nodeRunId>`, using the node run UUID from the current dispatch context; if you do not know it, run `loopctl task current` to check first. The CLI does not read or fill it in for you; ordinary messages and plain answers do not take this parameter. After a retry, the old node ID cannot be used for new questions.
- After receiving a `question`, prefer answering by option: `loopctl inbox reply <messageId> --answer 2`. The text can be omitted; when you need to explain, you can add `--text-file -` as well. You can also add `--option` to ask a new question (a task instance still carries node-run explicitly). Without an answer there must be text, and at most one of text/text-file.
- `already_answered`, `question_expired` and `question_closed` all exit with 8; look at `error.details.question` for the original question's latest state and existing answers. Do not treat closed as expired, and do not retry the answer automatically. An ordinary text message is different from an explicit option answer; do not release a task's wait on your own because of it.
- `question` (options/expiresAt/state/answer), `answerTo` and `notice` are output as is. When a question expires you receive a `notice.type=question_expired` notice along the original thread, sent by `@platform`; replying to such a notice contacts the other party of the original thread, so reply (with options) only when you need to ask again, and do not reply when nothing needs handling.
- Standalone task status notices are another kind of system message: read-only, not to be replied to; do not treat every platform notice as an original-thread notice. The platform cannot be a recipient; sending directly to @platform or replying to the platform is refused.

## General tasks

Task actions only accept in-platform identities. A project Manager can create tasks in its project and manage them; a node Agent can only submit the node currently dispatched to it, and the real permissions are verified by the server.

- The Manager first runs `loopctl task agents` to get the IDs, Prompts and CLI lists of the Agents available in the project. Workflow executors are given by Agent ID.
- The Manager uses `loopctl task list --limit 20` to page through the project's tasks; when the result has `nextCursor`, continue with `--cursor`.
- Create a task with `loopctl task create --from-file task.json`. The file is a formal `TaskCreateInput`: `actionId` (a UUID generated by the caller), `title`, `request`, `workflow`, and optionally `sourceInboxMessageId`. The workflow's `startStepId`, `steps` and `edges` are defined by the Manager; nodes contain a description, an Agent ID, input and output fields and source mappings, and field types are checked by the server. There are no built-in business templates; write reusable flows in the Manager's business Skill.
- View a task: `loopctl task get <taskId>`; a task instance uses `loopctl task current` to see its current task. The returned `task.currentNodeRunId` is the current node run ID, and `nodes` gives inputs, optional flow links and previous visits. Retries/loops create new node run IDs; keep the old IDs to recognize late submissions.
- `loopctl task history <taskId> <nodeRunId> --limit 20` shows the conversation of a given node; when `nextCursor` is not empty, continue with `--cursor`. A failed reply may carry `errorCode`/`errorMessage`, both kept as is; old replies without these fields can still be read, and do not infer success from an empty text.
- When done, run `loopctl task submit <taskId> <nodeRunId> --from-file output.json`, with JSON `{"actionId":"<UUID>","output":{…},"edgeId":"<chosen exit ID>"}`; the last node uses `"edgeId":null` explicitly. The output contains only the fields the node declares, and file results use platform artifact references.
- A successful submit only means the result is saved. The platform advances only after the current turn ends normally; if this turn ends abnormally, the task is blocked with the result kept, and a person or the Manager decides whether to retry. A node that neither submits a result nor has a pending question in this turn is blocked. Do not take the submit receipt to mean the task is complete.
- When you cannot complete it, run `loopctl task fail <taskId> <nodeRunId> --action-id <UUID> --reason-file reason.txt` and state the reason for the failure clearly.
- For the dedicated recovery node after the console switches accounts, `task submit` and `task fail` must explicitly add `--run-id <run-UUID-from-this-turns-prompt>`; the existing `--run` is accepted as a synonym, and the two cannot be passed together. Ordinary nodes can still omit it. Read the exact ID every turn and do not guess from the process environment or the latest run; when a write result is unknown, only query the original action, and do not change the ID or resend automatically. The run parameter of `submit` is not given again as a runId inside the JSON.
- When the dedicated recovery node waits on a question through the Inbox, besides `--option` and `--node-run` also explicitly give `--run-id <run-UUID-for-this-turn>` (compatible with `--run`); ordinary questions keep their original call, and ordinary messages do not get a node or run filled in automatically.
- After a block, the Manager can retry with `loopctl task retry <taskId> --action-id <UUID> --expected-node-run <nodeRunId> --note-file note.txt --yes` (the note can be omitted). It reuses the original copy and session and closes old unanswered questions; the Agent decides from the note whether to ask again.
- The current Manager cancels a task with `loopctl task cancel <taskId> --action-id <UUID> --yes`; people can also cancel from the console. Cancelling ends the execution, closes old unanswered questions and keeps the records; questions already answered or expired keep their real final state. Late answers do not revive the task, and ordinary messages sent to an ended task go to the Manager.

The model, Prompt, business Skills, allowed CLIs and model/CLI credentials in a task are fixed when the task starts; replacing or deleting them in the Agent configuration only affects new tasks, and retries do not refresh them. Old credentials revoked by the provider still stop working. After a server restart, nodes that were executing are blocked, and the execution is not replayed automatically.

Task questions and answers still go through the Inbox, and a node has at most one unfinished question at a time. After asking, ending this turn waits for the answer; there is no need to call wait. An ordinary Inbox message can wake you, but ordinary messages are not treated as answers to the original question automatically. For the exact usage of option questions, follow the Inbox command reference of the same version.

When a task instance that is not the current node, or that is blocked, is woken by the Inbox, only handle the incoming message; it cannot submit results for the current node another instance is responsible for, and it does not retry the task automatically; an ordinary message does not replace an explicit retry by a person or the Manager.

### Saving task artifacts

- `loopctl task artifact put <taskId> <nodeRunId> --action-id <UUID> --file ./result.txt --name result.txt --content-type text/plain` publishes a file explicitly and returns a `TaskArtifact`; an artifact-type field submits `{"artifactId":"<the returned TaskArtifact.id>"}`, not a local path or the whole artifact object. A successful upload does not submit the node for you.
- `loopctl task artifact list <taskId>` lists saved artifacts.
- `loopctl task artifact get <taskId> <artifactId> --out ./result.txt` downloads and verifies the SHA-256, and replaces the file only after it passes.

Files in the task's temporary directory do not become persistent artifacts just by existing; the directory is reclaimed when the task ends, and only explicitly saved artifacts can still be queried by reference.

Every write action uses its own `actionId`. When resending the same action by hand, keep the original ID, target and text unchanged, and do not reuse the ID for another action. Write requests are not retried automatically; a timeout may already have succeeded, so check the task and its results before deciding to resend. The CLI does not read the "current node" first to infer the write target for you.

### General JSON format examples

The following only illustrates the protocol format; the nodes and fields are filled in by the Manager each time as needed and are not a built-in platform flow. Replace `<new action UUID>` with this action's UUID and `<executor Agent ID>` with an ID returned by task agents, then save it as task.json:

```json
{
  "actionId": "<new action UUID>",
  "title": "General task",
  "request": {"goal": "What this work requires"},
  "workflow": {
    "startStepId": "work",
    "steps": [{
      "id": "work",
      "title": "Process",
      "description": "Complete the work from the input and submit result",
      "agentId": "<executor Agent ID>",
      "inputFields": [{"name": "goal", "type": "string", "required": true}],
      "outputFields": [{"name": "result", "type": "string", "required": true}],
      "inputs": {"goal": {"kind": "request", "field": "goal"}}
    }],
    "edges": []
  }
}
```

With several nodes, edges use `id/from/to/label`; loop edges have a `maxTraversals` limit. An input mapping can also be `{"kind":"node","nodeId":"<source node ID>","field":"<source output field>"}`, which reads the output of the source node's most recent successful completion. Field types are string, number, integer, boolean, object, array and artifact; required fields, unknown fields and mapping checks follow the server.

The output.json of the last node in this example looks like this (use another new UUID for actionId):

```json
{"actionId":"<new action UUID>","output":{"result":"The result"},"edgeId":null}
```

## Sleeping on your own

Regular Agents (including Managers) and Agents inside tasks can register a sleep for their own instance. Use the run UUID given in the platform's prompt for this turn, and choose a separate UUID for every new action:

```sh
loopctl sleep --run <run-UUID-for-this-turn> --seconds 120 --action-id <action-UUID> --reason "Waiting for an external result"
loopctl sleep status
```

A sleep is an idle arrangement registered with the platform, not a blocking shell sleep. A successful receipt with `endTurn:true` means you then end the current turn normally; `requested` only means it is registered, and the current turn is still busy. The sleep starts, and wakeAt is computed, only after the current turn finishes successfully and its session is saved; a failure or interruption does not make the registration take effect. Do not keep holding the execution with a shell sleep, and do not take the registration receipt to mean you are already asleep or the node is done.

Once in a sleep, you count as idle. An existing queue or new messages (including Inbox messages with the default queue) can wake you early and cancel this timer; the old timer will not wake you again. A server restart keeps the original wakeAt and does not restart the timer. The wake-up turn continues the original conversation, and after waking you decide yourself whether to sleep again. A sleep in an ordinary conversation inside a task does not retry or advance the workflow, and does not answer or close the original question.

`sleep status` is a read-only query of the current run and the active sleep, and returns null when there is none. Registration does not accept a target Agent/task; the identity comes from the current token. The CLI does not query implicitly, replace the run ID or generate a substitute action, and does not retry the POST automatically. When the network result is unknown it may already be registered, so check the status first; resending the same action by hand must keep the same action-id, run, seconds and reason. A status query is not grounds for overriding and retrying with a different run ID. A 409 `sleep_conflict` is returned as is and ends with exit code 8.

## Context folders

You can read and write two layers of context: your own folder (which is your working directory), and the project context folder (shared by the Agents and people in the project; for a platform Agent, the global context folder). The absolute paths of both folders are in the "Folders and context" section of the system prompt; go by those, and do not build paths with `~`. Each layer's entry file is `AGENTS.md` at the folder's root: Pi loads it when it starts, and changes appear in the system prompt only after the next restart; read other files yourself when needed. An Agent in a task gets a copy of its own folder made when the task started, which is reclaimed when the task ends; the project context folder is shared live and not copied.

When to write (your own judgment):
- a person corrected you or gave you a lasting requirement;
- you made a decision that constrains later work;
- facts other Agents need to know;
- handovers; when an Agent in a task submits a node result, write down the conclusions later nodes or other tasks need.

Do not write: process logs, in-progress state, anything you can find in the code, git or run records, or any credentials.

Which layer: things that concern only you go into your own folder; things other Agents also need go into the project (or global) context folder.

How to organize it: one file per topic; edit the existing file instead of writing another copy; treat `AGENTS.md` as the table of contents and record the most important conclusions there; whoever adds or removes files updates `AGENTS.md` too; fix or delete outdated content promptly. There are no locks; when several people edit at once the last write wins, so read the latest content before writing.

## Output and exit codes

Each call writes exactly one JSON object to standard output:

- Success: `{"ok":true,"data":…,"warnings":[…],"meta":{…}}`, where `data` is the result;
- Failure: `{"ok":false,"error":{"code":…,"category":…,"message":…},"warnings":[…],"meta":{…}}`;
- `warnings` holds notes (for example, a version mismatch) and does not affect the result.

| Exit code | Category | Meaning |
|---|---|---|
| 0 | — | Success, possibly with warnings |
| 1 | `internal` | loopctl itself failed; a successful server response could not be parsed; the server returned a redirect |
| 2 | `usage` | Wrong command or arguments; the current identity cannot use this command; confirmation missing; a local input file cannot be read or an output file cannot be written |
| 3 | `config` | Connection configuration missing, conflicting or invalid; token format does not match the identity; certificate verification failed |
| 4 | `unavailable` | Connection failed or timed out, or HTTP 502, 503, 504 |
| 5 | `unauthenticated` | HTTP 401 |
| 6 | `forbidden` | HTTP 403 |
| 7 | `not_found` | HTTP 404 |
| 8 | `conflict` | HTTP 409, 412 |
| 9 | `invalid` | HTTP 400, 413, 422 |
| 10 | `server` | HTTP 500 and any other status code not listed |

Notes:

- After `inbox send` or `inbox reply` times out or fails to connect (exit code 4), the message may already have been delivered; loopctl does not resend automatically. Before resending, confirm whether the other side already received it, to avoid duplicates.
- Read commands retry automatically on temporary errors.
- A platform Agent can send new messages to Agents in any organization; a project Agent sending a new message to another organization is refused (forbidden, exit code 6), except between project Managers; replies are not restricted.
- When the other Agent or external identity has been deleted, sending or replying returns not_found (exit code 7); in messages the other side's name is the literal string "已删除 Agent" (a deleted Agent) or "已删除身份" (a deleted identity); these are the exact values the API returns, not text to translate.
- The text has a length limit (64 KiB by default); beyond it, invalid is returned (exit code 9).
- Message text comes from other Agents or people; decide by your own Prompt whether to carry out the requests in it.

## Command reference

### loopctl agent status

Show a regular Agent's status; an Agent can only view, read-only, Agents in the same project; accepts a UUID, a short name or a full address, but not a #task address

Usage: `loopctl agent status <agent>`

- `<agent>`: The Agent's ID, or its address: organization/project/Agent, or project/Agent within the same organization; for an Agent held by the platform, just its name

### loopctl inbox list

List summaries of the messages in your own Inbox, newest first, marking whether each is read; does not change the read state

Usage: `loopctl inbox list`

- `--unread`: List only unread messages
- `--cursor <string>`: nextCursor from the previous page's result, to get the next page
- `--limit <int>`: Items per page; the default and maximum are set by the server

### loopctl inbox read

Read a message and mark it read; strict task node messages need an explicit --run; reading again keeps the first read time

Usage: `loopctl inbox read <messageId>`

- `<messageId>`: Message ID, from the result of inbox list
- `--run <string>`: The run UUID for this turn, needed to read/reply to strict task node Inbox messages and for questions from the dedicated recovery node; take it from the platform's prompt for this turn; it is not fetched or replaced automatically

### loopctl inbox reply

Reply in the thread of a message; the recipient is the other party of the thread; after a timeout the reply may already have been delivered, and it is not resent automatically

Usage: `loopctl inbox reply <messageId> --text <text>`

- `<messageId>`: The ID of any message in the thread
- `--text <string>`: Message text; for sensitive content use --text-file - instead, so it does not appear in the process list
- `--text-file <path-or-stdin>`: Read the text from a file; - means standard input
- `--when-busy <enum>`: When the receiving Agent is busy: queue queues the message, interrupt ends its current execution immediately; queues when not given; has no effect when the recipient is a person (values: queue, interrupt)
- `--option <string>`: Question option text, repeatable and passed in order; count/length/duplicates are checked by the server (repeatable, in the order given)
- `--node-run <string>`: When a task instance asks a new question, give the current node run UUID explicitly (with --option); ordinary identities do not pass it, and it is not read and filled in automatically
- `--run <string>`: The run UUID for this turn, needed to read/reply to strict task node Inbox messages and for questions from the dedicated recovery node; take it from the platform's prompt for this turn; it is not fetched or replaced automatically
- `--answer <string>`: Option ID of the original question; with it the text can be omitted, or you can also add text or options for a new question
- At most one of these: --text, --text-file
- At least one of these is required: --text, --text-file, --answer

### loopctl inbox send

Send a message to an address; every send starts a new thread; after a timeout the message may already have been delivered, and it is not resent automatically

Usage: `loopctl inbox send <address> --text <text>`

- `<address>`: Recipient address: for an Agent, organization/project/Agent (project/Agent within the same organization; append #taskID for an Agent inside a task); for an Agent held by the platform, just its name; @console for the console operator; @<identity name> for an external identity
- `--text <string>`: Message text; for sensitive content use --text-file - instead, so it does not appear in the process list
- `--text-file <path-or-stdin>`: Read the text from a file; - means standard input
- `--when-busy <enum>`: When the receiving Agent is busy: queue queues the message, interrupt ends its current execution immediately; queues when not given; has no effect when the recipient is a person (values: queue, interrupt)
- `--option <string>`: Question option text, repeatable and passed in order; count/length/duplicates are checked by the server (repeatable, in the order given)
- `--node-run <string>`: When a task instance asks a new question, give the current node run UUID explicitly (with --option); ordinary identities do not pass it, and it is not read and filled in automatically
- `--run <string>`: The run UUID for this turn, needed to read/reply to strict task node Inbox messages and for questions from the dedicated recovery node; take it from the platform's prompt for this turn; it is not fetched or replaced automatically
- `--run-id <string>`: The exact run UUID for this turn; synonym of --run, and the two cannot be given together; required for the dedicated recovery node
- Exactly one of these is required: --text, --text-file

### loopctl project list

For platform Agents, discover projects and their current Manager; for external identities, the project list (default project first)

Usage: `loopctl project list`

### loopctl schema

Print a machine-readable description of the commands: all commands, error categories and exit codes, identities and connection parameters, and global options; needs no connection configuration and sends no request

Usage: `loopctl schema`

- `<path>`: Command path, for example inbox list; when the path is a command group, prints its direct subcommands; when omitted, prints everything
- `--identity <enum>`: Print only the commands available to this identity (values: agent, external)

### loopctl sleep

Register a sleep for yourself; it takes effect only after the current turn then ends normally; on a network failure it may already be registered, so check the status first

Usage: `loopctl sleep --run <run> --seconds 1 --action-id <action-id>`

- `--run <string>`: The run UUID from the platform's prompt for this turn; it is not queried or replaced with another run
- `--seconds <int>`: Seconds to wait, 1..2147483647; counted from when the current turn finishes successfully
- `--action-id <string>`: UUID of this action; when resending the same action by hand keep the same ID and content, and never reuse it for another action
- `--reason <string>`: Optional note, subject to the server's message text length limit

### loopctl sleep status

Read-only query of your current run and active sleep; null when there is none; does not create or reset a sleep

Usage: `loopctl sleep status`

### loopctl task agents

For the current project Manager: show the safe directory of Agents available in the project

Usage: `loopctl task agents`

### loopctl task artifact get

Download a task artifact to the given file and save it atomically after the SHA-256 check passes

Usage: `loopctl task artifact get <taskId> <artifactId> --out <out>`

- `<taskId>`: Task ID, from the result of task create/get/current
- `<artifactId>`: Platform artifact ID, from task artifact list/put
- `--out <path>`: Local output file path; replaces the file of the same name on success

### loopctl task artifact list

List the metadata of the artifacts the platform saved, from the authorized task details

Usage: `loopctl task artifact list <taskId>`

- `<taskId>`: Task ID, from the result of task create/get/current

### loopctl task artifact put

For the current node: explicitly save a file artifact and return a platform-held reference; does not submit the node automatically

Usage: `loopctl task artifact put <taskId> <nodeRunId> --action-id <action-id> --file <file>`

- `<taskId>`: Task ID, from the result of task create/get/current
- `<nodeRunId>`: Node run ID, from the current task; after a retry or loop, the new ID must be used
- `--action-id <string>`: UUID of this action; when resending the same action by hand keep the same ID and content, and never reuse it for another action
- `--file <path>`: Local artifact file
- `--name <string>`: Artifact name; defaults to the local file name
- `--content-type <string>`: Artifact MIME type

### loopctl task cancel

For the current project Manager: cancel the task; ends the execution, closes unanswered questions and keeps the history

Usage: `loopctl task cancel <taskId> --action-id <action-id>`

- `<taskId>`: Task ID, from the result of task create/get/current
- `--action-id <string>`: UUID of this action; when resending the same action by hand keep the same ID and content, and never reuse it for another action
- `--yes`: Confirm a deletion or overwrite; without it no request is sent

### loopctl task create

For the Manager: create a task with a fixed workflow; after a timeout it may already be created, so check before deciding whether to resend

Usage: `loopctl task create --from-file <from-file>`

- `--from-file <path-or-stdin>`: Formal TaskCreateInput/TaskSubmitInput JSON file; - reads standard input; contains the caller-generated actionId

### loopctl task current

For a task instance: show its current task; regular identities have no current task

Usage: `loopctl task current`

### loopctl task fail

Declare that the current node failed, keeping the reason; the task blocks until a person or the Manager handles it

Usage: `loopctl task fail <taskId> <nodeRunId> --action-id <action-id> --reason-file <reason-file>`

- `<taskId>`: Task ID, from the result of task create/get/current
- `<nodeRunId>`: Node run ID, from the current task; after a retry or loop, the new ID must be used
- `--action-id <string>`: UUID of this action; when resending the same action by hand keep the same ID and content, and never reuse it for another action
- `--run <string>`: The exact run UUID given in the platform's prompt for this turn; required for the dedicated recovery node; it does not read the latest run or retry automatically
- `--run-id <string>`: The exact run UUID for this turn; synonym of --run, and the two cannot be given together; required for the dedicated recovery node
- `--reason-file <path-or-stdin>`: UTF-8 file with the failure reason; - reads standard input; cannot be empty

### loopctl task get

Show a task you may access, with its node records, outputs and saved artifacts

Usage: `loopctl task get <taskId>`

- `<taskId>`: Task ID, from the result of task create/get/current

### loopctl task history

Page through the conversation of a node run, keeping execution states and specific errors as is

Usage: `loopctl task history <taskId> <nodeRunId>`

- `<taskId>`: Task ID, from the result of task create/get/current
- `<nodeRunId>`: Node run ID, from the current task; after a retry or loop, the new ID must be used
- `--cursor <string>`: nextCursor from the previous page's result, to get the next page
- `--limit <int>`: Items per page; the default and maximum are set by the server

### loopctl task list

For the current project Manager: page through the project's tasks

Usage: `loopctl task list`

- `--cursor <string>`: nextCursor from the previous page's result, to get the next page
- `--limit <int>`: Items per page; the default and maximum are set by the server

### loopctl task retry

For the current project Manager: retry the blocked node; reuses the task copy/session/snapshot and closes the last unanswered question

Usage: `loopctl task retry <taskId> --action-id <action-id> --expected-node-run <expected-node-run>`

- `<taskId>`: Task ID, from the result of task create/get/current
- `--action-id <string>`: UUID of this action; when resending the same action by hand keep the same ID and content, and never reuse it for another action
- `--expected-node-run <string>`: UUID of the expected blocked node run; the server refuses if the node has changed
- `--note-file <path-or-stdin>`: Optional retry note; - reads standard input
- `--yes`: Confirm a deletion or overwrite; without it no request is sent

### loopctl task submit

Submit the current node's output and exit; after it is saved, the current turn must still end normally before the platform advances

Usage: `loopctl task submit <taskId> <nodeRunId> --from-file <from-file>`

- `<taskId>`: Task ID, from the result of task create/get/current
- `<nodeRunId>`: Node run ID, from the current task; after a retry or loop, the new ID must be used
- `--from-file <path-or-stdin>`: Formal TaskCreateInput/TaskSubmitInput JSON file; - reads standard input; contains the caller-generated actionId
- `--run <string>`: The exact run UUID given in the platform's prompt for this turn; required for the dedicated recovery node; it does not read the latest run or retry automatically
- `--run-id <string>`: The exact run UUID for this turn; synonym of --run, and the two cannot be given together; required for the dedicated recovery node

### loopctl version

Print the local loopctl version, operating system, architecture and Go version; sends no request

Usage: `loopctl version`

### loopctl whoami

Show the type, address and name of the current identity

Usage: `loopctl whoami`
