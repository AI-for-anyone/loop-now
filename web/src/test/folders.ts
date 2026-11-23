import type { FolderEntry, FolderSource, GitPull } from '@/features/folders/folder-source'
import { joinPath, nameOf, parentOf, segmentsOf, within } from '@/features/folders/folder-source'

// In-memory demo folders, shaped like /agents/{agentId}/folder and related
// endpoints: shared by the file manager tests and the UI mockup screenshots.

type Node =
  | { kind: 'dir'; children: Map<string, Node>; modifiedAt: string }
  | { kind: 'file'; bytes: Blob; modifiedAt: string; image?: string }
  | { kind: 'symlink'; target: string; modifiedAt: string }

const at = (minutesAgo: number) => new Date(Date.UTC(2026, 9, 25, 8, 30) - minutesAgo * 60_000).toISOString()
const dir = (minutesAgo = 600): Node => ({ kind: 'dir', children: new Map(), modifiedAt: at(minutesAgo) })
const text = (body: string, minutesAgo = 120): Node => ({ kind: 'file', bytes: new Blob([body], { type: 'text/plain' }), modifiedAt: at(minutesAgo) })
const binary = (size: number, minutesAgo = 120): Node => { const bytes = new Uint8Array(size); bytes[1] = 0; bytes[0] = 0x7f; return { kind: 'file', bytes: new Blob([bytes]), modifiedAt: at(minutesAgo) } }

const AGENTS_MD = `# Working notes for server

You develop and maintain the Loop Now server (Go).

## Before you start
- Read \`handoff/notes.md\` first to see where the last round stopped.
- Run \`go test ./internal/server/...\` before changing anything, and note the failures that are already there.

## Conventions
- \`api/openapi.yaml\` is the source of truth: change the contract before the endpoint.
- Don't commit or deploy; report to the Manager in the Inbox when done.
- Run only the tests related to the change, and write the results into the handoff notes as they are.
`

const GO_MAIN = `// loop-server is the Loop Now server. All configuration comes from environment variables.
package main

import (
\t"context"
\t"fmt"
\t"os"
\t"os/signal"
\t"syscall"

\t"loop-now/internal/server/app"
\t"loop-now/internal/server/config"
)

func main() {
\tcfg, err := config.Load(os.Getenv)
\tif err != nil {
\t\tfmt.Fprintln(os.Stderr, "invalid configuration, refusing to start:", err)
\t\tos.Exit(2)
\t}
\tctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
\tdefer stop()

\ta, err := app.Build(ctx, cfg, config.NewLogger(os.Stderr, cfg.LogFormat, cfg.LogLevel), app.Options{})
\tif err != nil {
\t\tos.Exit(1)
\t}
\tgo func() { _ = a.Serve() }()
\t<-ctx.Done()
\t_ = a.Shutdown(context.Background())
}
`

const BUDGET_GO = `package tasks

// Budget records a task's usage limit and usage so far; write order: ledger first, then events.
type Budget struct {
\tLimit int64
\tUsed  int64
}

// Spend deducts usage; over the limit it returns false and leaves the usage unchanged.
func (b *Budget) Spend(n int64) bool {
\tif b.Used+n > b.Limit {
\t\treturn false
\t}
\tb.Used += n
\treturn true
}
`

const QUERIES_TS = `import { useQuery } from '@tanstack/react-query'
import { api, call } from './client'

export const keys = {
  agent: (agentId: string) => ['agent', agentId] as const,
  messages: (agentId: string) => ['messages', agentId] as const,
}

// An Agent's details, fetched again by ID when switching Agents.
export function useAgent(agentId: string) {
  return useQuery({
    queryKey: keys.agent(agentId),
    queryFn: ({ signal }) => call(api.GET('/agents/{agentId}', { signal, params: { path: { agentId } } })),
  })
}
`

const PNG_SVG = 'data:image/svg+xml;utf8,' + encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" width="480" height="300" viewBox="0 0 480 300"><rect width="480" height="300" fill="#F3EFE6"/><rect x="40" y="40" width="400" height="220" rx="18" fill="#E3E8F8"/><rect x="70" y="200" width="340" height="36" rx="18" fill="#fff" stroke="#18265E" stroke-opacity=".6" stroke-width="2"/><rect x="90" y="150" width="300" height="40" rx="10" fill="#FBF9F4" stroke="#18265E" stroke-opacity=".28"/><circle cx="380" cy="218" r="12" fill="#18265E"/></svg>')

function put(root: Node, path: string, node: Node) {
  let cur = root
  const parts = segmentsOf(path)
  parts.forEach((part, index) => {
    if (cur.kind !== 'dir') throw new Error(`not a dir: ${part}`)
    if (index === parts.length - 1) cur.children.set(part, node)
    else {
      if (!cur.children.has(part)) cur.children.set(part, dir())
      cur = cur.children.get(part)!
    }
  })
}

function workspace(): Node {
  const root = dir(5)
  put(root, 'AGENTS.md', text(AGENTS_MD, 30))
  put(root, 'README.md', text('# loop-now\n\nThe Loop Now server and console.\n', 2000))
  put(root, '.gitignore', text('node_modules/\n.local-build/\n*.log\n', 9000))
  put(root, 'go.mod', text('module loop-now\n\ngo 1.25\n', 4000))
  put(root, 'Makefile', text('test:\n\tgo test ./...\n', 4000))
  put(root, 'cmd/loop-server/main.go', text(GO_MAIN, 600))
  put(root, 'internal/server/app/app.go', text('package app\n', 300))
  put(root, 'internal/server/store/store.go', text('package store\n', 900))
  put(root, 'internal/server/tasks/budget.go', text(BUDGET_GO, 12))
  put(root, 'internal/server/tasks/budget_test.go', text('package tasks\n', 12))
  put(root, 'web/package.json', text('{\n  "name": "loop-now-web",\n  "private": true\n}\n', 3000))
  put(root, 'web/vite.config.ts', text("import { defineConfig } from 'vite'\n\nexport default defineConfig({})\n", 3000))
  put(root, 'web/src/main.tsx', text("import { createRoot } from 'react-dom/client'\n", 400))
  put(root, 'web/src/api/client.ts', text("export const api = {}\n", 400))
  put(root, 'web/src/api/queries.ts', text(QUERIES_TS, 45))
  put(root, 'web/src/features/chat/ChatPage.tsx', text('export function ChatPage() {}\n', 60))
  const names = ['@babel', '@esbuild', '@radix-ui', '@tanstack', '@types', 'acorn', 'ansi-styles', 'chalk', 'clsx', 'debug', 'esbuild', 'eslint', 'glob', 'highlight.js', 'jsdom', 'lucide-react', 'mermaid', 'msw', 'nanoid', 'postcss', 'react', 'react-dom', 'react-markdown', 'rollup', 'semver', 'tailwindcss', 'typescript', 'vite', 'vitest', 'zod']
  for (let i = 0; i < 186; i++) {
    const name = i < names.length ? names[i] : `pkg-${String(i).padStart(3, '0')}`
    put(root, `web/node_modules/${name}/package.json`, text(`{ "name": "${name}" }\n`, 3000))
  }
  put(root, 'handoff/notes.md', text('# Handoff\n\n- The budget ledger write order is done; the tests still need a run.\n', 8))
  put(root, 'docs/design.md', text('# Design\n', 5000))
  put(root, 'docs/screenshots/queue.png', { kind: 'file', bytes: new Blob([new Uint8Array(48213)], { type: 'image/png' }), modifiedAt: at(80), image: PNG_SVG })
  put(root, 'data/fixtures.bin', binary(262144, 7000))
  put(root, 'logs/server.log', text('x'.repeat(1_400_000), 1))
  put(root, 'current-notes.md', { kind: 'symlink', target: 'handoff/notes.md', modifiedAt: at(8) })
  put(root, 'scripts/release.sh', text('#!/bin/sh\nset -eu\ngo build ./cmd/...\n', 6000))
  return root
}

// Project shared folder: no AGENTS.md yet; more than 160 folders at the top level, to exercise paging.
function project(): Node {
  const root = dir(60)
  const areas = ['billing', 'search', 'auth', 'gateway', 'ledger', 'notify', 'report', 'import']
  for (let i = 1; i <= 164; i++) put(root, `svc-${String(i).padStart(3, '0')}-${areas[i % areas.length]}/README.md`, text('# Service notes\n', 4000 + i))
  put(root, 'conventions.md', text('# Project conventions\n\n- Run the tests before committing.\n', 90))
  put(root, 'glossary.md', text('# Glossary\n', 900))
  return root
}

function global(): Node {
  const root = dir(240)
  put(root, 'AGENTS.md', text('# Global conventions\n\nEvery Agent reads this.\n\n- Reply in English.\n- Never print or commit credentials.\n', 600))
  put(root, 'reference/style.md', text('# Style\n', 3000))
  put(root, 'reference/api-errors.md', text('# Error codes\n', 3000))
  put(root, 'templates/handoff.md', text('# Handoff template\n', 3000))
  return root
}

const PRESETS = { workspace, project, global }

export interface MockFolderOptions {
  key: string
  preset: keyof typeof PRESETS
  latency?: number
  // Entries per page; the real endpoint defaults to 500, the demo uses fewer so that paging is visible.
  pageSize?: number
  // Only Agent folders can pull from Git. Pulling fails when the address contains "fail".
  git?: boolean
  gitDuration?: number
  // How long each file takes to upload.
  uploadDuration?: number
}

export function createMockFolder({ key, preset, latency = 120, pageSize = 100, git = false, gitDuration = 4000, uploadDuration = 1200 }: MockFolderOptions): FolderSource {
  const root = PRESETS[preset]()
  const pulls = new Map<string, GitPull>()
  const wait = (ms = latency) => new Promise((resolve) => setTimeout(resolve, ms))
  const find = (path: string): Node | null => {
    let cur: Node = root
    for (const part of segmentsOf(path)) {
      if (cur.kind !== 'dir') return null
      const next = cur.children.get(part)
      if (!next) return null
      cur = next
    }
    return cur
  }
  const ensureDir = (path: string): Extract<Node, { kind: 'dir' }> => {
    let cur: Node = root
    for (const part of segmentsOf(path)) {
      if (cur.kind !== 'dir') throw new Error(`Not a folder: ${part}`)
      if (!cur.children.has(part)) cur.children.set(part, dir(0))
      cur = cur.children.get(part)!
    }
    if (cur.kind !== 'dir') throw new Error(`Not a folder: ${path}`)
    return cur
  }
  const entryOf = (path: string, node: Node): FolderEntry => ({
    name: nameOf(path), path, type: node.kind, size: node.kind === 'file' ? node.bytes.size : null, modifiedAt: node.modifiedAt, symlinkTarget: node.kind === 'symlink' ? node.target : null,
  })
  const conflict = (name: string) => Object.assign(new Error(`A file or folder with this name already exists: ${name}`), { code: 'path_conflict' })
  return {
    key,
    async list(path, cursor) {
      await wait()
      const node = find(path)
      if (!node || node.kind !== 'dir') throw new Error(`Folder not found: ${path || '/'}`)
      const all = [...node.children.entries()].map(([name, child]) => entryOf(joinPath(path, name), child)).sort((a, b) => (a.type === 'dir') === (b.type === 'dir') ? a.name.localeCompare(b.name) : a.type === 'dir' ? -1 : 1)
      const start = cursor ? Number(cursor) : 0
      return { path, entries: all.slice(start, start + pageSize), nextCursor: start + pageSize < all.length ? String(start + pageSize) : null }
    },
    async read(path) {
      await wait()
      let node = find(path)
      if (node?.kind === 'symlink') node = find(node.target)
      if (!node || node.kind !== 'file') throw new Error(`File not found: ${path}`)
      return node.bytes
    },
    async write(path, body, create) {
      await wait()
      const parent = ensureDir(parentOf(path))
      const existing = parent.children.get(nameOf(path))
      if (create && existing) throw conflict(nameOf(path))
      if (existing && existing.kind !== 'file') throw new Error('The target is a folder or a symlink and cannot be written')
      const node: Node = { kind: 'file', bytes: typeof body === 'string' ? new Blob([body], { type: 'text/plain' }) : body, modifiedAt: new Date().toISOString() }
      parent.children.set(nameOf(path), node)
      return entryOf(path, node)
    },
    async mkdir(path) {
      await wait()
      const parent = ensureDir(parentOf(path))
      if (parent.children.has(nameOf(path))) throw conflict(nameOf(path))
      const node = dir(0)
      parent.children.set(nameOf(path), node)
      return entryOf(path, node)
    },
    async move(from, to) {
      await wait()
      const node = find(from)
      if (!node || from === '') throw new Error(`Not found: ${from}`)
      if (within(to, from)) throw new Error('Can\'t move a folder into itself')
      const target = ensureDir(parentOf(to))
      if (target.children.has(nameOf(to))) throw conflict(nameOf(to))
      ;(find(parentOf(from)) as Extract<Node, { kind: 'dir' }>).children.delete(nameOf(from))
      target.children.set(nameOf(to), node)
      return entryOf(to, node)
    },
    async remove(path) {
      await wait()
      if (path === '') throw new Error('Can\'t delete the root folder')
      ;(find(parentOf(path)) as Extract<Node, { kind: 'dir' }>).children.delete(nameOf(path))
    },
    async upload(target, item, onProgress) {
      const steps = 6
      for (let i = 1; i <= steps; i++) {
        await wait(uploadDuration / steps)
        onProgress(Math.round((item.file.size * i) / steps))
      }
      put(root, joinPath(target, item.path), { kind: 'file', bytes: item.file, modifiedAt: new Date().toISOString() })
    },
    contentURL: (path) => {
      const node = find(path)
      return node?.kind === 'file' && node.image ? node.image : `#download:${encodeURIComponent(path)}`
    },
    ...(git ? {
      git: {
        async start({ url, branch, path }) {
          await wait()
          const target = find(path)
          if (target && (target.kind !== 'dir' || target.children.size > 0)) throw conflict(nameOf(path) || '/')
          const pull: GitPull = { id: crypto.randomUUID(), agentId: key, url, branch, path, status: 'running', errorCode: null, errorMessage: null, createdAt: new Date().toISOString(), finishedAt: null }
          pulls.set(pull.id, pull)
          setTimeout(() => {
            if (url.includes('fail')) Object.assign(pull, { status: 'failed', errorCode: 'clone_failed', errorMessage: "fatal: could not read Username for 'https://github.com': terminal prompts disabled", finishedAt: new Date().toISOString() })
            else {
              put(root, joinPath(path, 'README.md'), text('# budget-ledger\n\nThe ledger service.\n', 0))
              put(root, joinPath(path, 'ledger/ledger.go'), text('package ledger\n', 0))
              Object.assign(pull, { status: 'succeeded', finishedAt: new Date().toISOString() })
            }
          }, gitDuration)
          return { ...pull }
        },
        // Newest first.
        async list() {
          await wait(40)
          return [...pulls.values()].reverse().map((pull) => ({ ...pull }))
        },
      },
    } : {}),
  }
}
