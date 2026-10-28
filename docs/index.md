---
layout: home

hero:
  name: Loop Now
  text: A local multi-agent workspace
  tagline: Connect CLI and MCP tools, subscribe to events, and shape how your agents work — while keeping projects, conversations, and tasks in one place.
  image:
    src: /favicon.svg
    alt: Loop Now logo
    width: 140
  actions:
    - theme: brand
      text: Get started
      link: /guide/getting-started
    - theme: alt
      text: GitHub
      link: https://github.com/AI-for-anyone/loop-now
    - theme: alt
      text: 简体中文
      link: /zh/
    - theme: alt
      text: v0.1.0
      link: https://github.com/AI-for-anyone/loop-now/releases

features:
  - icon: 🤝
    title: Build a team
    details: Organize agents by project. Give each a model, thinking level, instructions, Skills, repositories, and tool access. Designate a Manager to coordinate the project.
  - icon: 🧠
    title: Keep work in context
    details: Continue an agent's regular conversation and workspace. Tasks use separate copies, with the same agent reusing its task workspace and session across steps.
  - icon: 📬
    title: Coordinate through Inbox
    details: Agents and people exchange messages, ask questions, and reply. Choose queue or interrupt when an agent is busy; sleeping agents can wake when messages arrive.
  - icon: 🔀
    title: Run visible workflows
    details: Follow task nodes, conditional branches, bounded review loops, retries, and saved artifacts. A task's workflow and captured configuration stay fixed after it starts.
  - icon: 🧩
    title: Extend through plugins
    details: Bring CLI and MCP tools, event hooks, accounts, permissions, and Skills into one package. Add the integrations your team needs.
  - icon: 🔍
    title: See what happened
    details: Stream replies, send images, render Markdown and Mermaid, and inspect model usage, tool commands, timing, and failures in each reply's run timeline.
---

<div class="ln-hero-image">

![Loop Now console: an agent conversation with rendered diagrams and a compact message composer](/assets/console.png)

</div>

<p class="ln-caption">The actual console with demonstration data. The interface currently uses Simplified Chinese.</p>

## From implementation to review, in one conversation

Create a Manager, a Developer, and a Reviewer. Assign their repositories, models, and allowed tools, then ask the Manager to coordinate a change:

> Implement this feature, have the Reviewer check it, and return the result. Ask me through Inbox if a decision is needed.

A workflow can move from development to review and back for a bounded number of revisions. Each participant works in its task context; you can inspect messages and artifacts throughout. Your project instructions and Skills define the actual process — the platform does not prescribe a particular coding, QA, or deployment policy.

## Plugins are the extension point

A plugin is one directory whose `package.json` is the manifest. The **agent half** is a standard [Pi package](https://pi.dev/packages) loaded into each granted agent's Pi process; the **platform half** runs in a long-lived Node host next to the server, subscribing to platform events with a scoped token.

```ts twoslash
import { createDelivery, type PluginAPI } from '@loop-now/plugin-sdk'

export default function register(api: PluginAPI) {
  const delivery = createDelivery(api)
  api.on('task.completed', async (event) => {
    const { url } = api.config.get<{ url?: string }>(event.projectId)
    if (url) await delivery.deliver({ target: url, body: event, eventId: event.id, eventType: event.type })
  })
}
```

The manifest is authoritative: subscribing to an event, or calling an API scope, that the manifest does not declare fails — and install shows both halves, the events, and the permissions before anything runs. [Read the plugin overview →](/plugins/)

## Early-stage personal edition

This project targets a **single local server**. The console has no login and is intended for loopback access. Credentials are stored in plaintext in your own PostgreSQL — protect database access and backups. Model and tool providers may still receive data when you use their services.

Loop Now runs on your machine with [Pi](https://github.com/earendil-works/pi) as its execution engine. You choose the models, credentials, repositories, and tools each agent may use.
