# Concepts

Loop Now is built around a small set of ideas. This page explains how they fit together; the details live in the repository's [README](https://github.com/AI-for-anyone/loop-now) and specs.

## Organization, Project, Agent

The hierarchy is **Organization → Project → Agent**, plus platform-wide agents.

- A **project** groups agents, accounts, repositories, plugins, and shared context.
- An **agent** has its own model, thinking level, instructions, Skills, tool access, and a persistent working directory that is also its context.
- A project can designate a **Manager**. The Manager is a regular agent with a coordination role: you talk to it, and it delegates through the project's task workflow.

People configure permissions; a Manager can only choose agents that are already configured and cannot grant new access on the fly.

## Conversations and context

An agent keeps its **regular conversation** and workspace across turns. Its working directory — the agent folder — is its context: the entry point is an `AGENTS.md` at the folder root, and the agent manages the rest of the files itself.

**Tasks** use separate copies. When a task starts, the platform clones the agent folder (APFS clone copy on macOS) and fixes the workflow, configuration, permission, and account versions. The same agent reuses its task workspace and session across a task's steps; when the task ends, the execution resources are recycled and the records, archived session, and artifacts are kept.

## Inbox: queue or interrupt

Agents and people coordinate through the **Inbox**:

- Send a message to a busy agent and it **queues**, in order, listed above the composer's input box.
- Any queued item can be **promoted to an interrupt**, edited, or cancelled; you can also stop the current reply and let the queue continue.
- An agent can **sleep** — an idle wait that new messages can wake early.
- When an agent needs a decision, it **asks a question**; you answer inline or from the pending list, and the reply marks the thread read.

## Tasks and workflows

A task is a fixed **workflow** of nodes executed by agents. Workflows support:

- linear sequences, conditional branches, and bounded review loops (parallel join is not supported yet),
- retries, cancellation, and saved artifacts per node,
- **questions** that pause a node until a person answers,
- node conversations: each task node has its own message thread with the people and agents involved.

The workflow and captured configuration are **frozen** when the task starts. Ordinary retries do not refresh credentials.

## Accounts and permissions

The project stores **model and CLI accounts** centrally; agents take them according to permission and selection rules. Connecting a provider account is an explicit action with a verification step, and tool integrations are granted per agent.

The personal edition has **no master key**: credentials are stored in plaintext in PostgreSQL. Protect database access and backups, and treat the console's loopback-only, login-less design accordingly.

## Plugins

Plugins are the main extension point — see the [plugin overview](/plugins/) for the two-halves model, the host capabilities, and how installation and authorization work.
