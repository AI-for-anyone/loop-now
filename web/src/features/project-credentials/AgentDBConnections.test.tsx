import { act, screen, waitFor } from '@testing-library/react'
import { useState } from 'react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultProject, errorBody, makeAgent } from '@/test/api'
import { emptySelections, projectAccount } from '@/test/project-credentials'
import { renderConfig } from '@/test/config'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { ScopeContext } from '@/features/projects/scope-context'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { dbConnectionKey } from '@/api/db-connections'
import { AgentDBConnections } from './AgentDBConnections'
import { CredentialFields } from './CredentialFields'
import { AccountImpact } from './ProjectAccounts'
import { CliToolStatus } from '@/features/agent-config/CredentialStatus'

const agent = makeAgent()
const a = projectAccount({ purpose: 'cli', provider: null, cli: 'psql', name: 'Connection A', authMethod: null })
const b = projectAccount({ ...a, id: '10000000-0000-7000-8000-000000000002', name: 'Connection B' })
function connections(overrides: Partial<Schemas['DBConnections']> = {}): Schemas['DBConnections'] {
  return { agentId: agent.id, projectId: defaultProject.id, cli: 'psql', configVersion: 1, credentialsRevision: 1, mode: 'explicit_set', accountIds: [], connections: [], status: 'not_configured', ...overrides }
}
function renderDB(read: () => Schemas['DBConnections'], accounts = [a, b]) {
  server.use(
    http.get(apiUrl('/agents/:agentId/db-connections/:cli'), () => HttpResponse.json(read())),
    http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accounts })),
  )
  return renderRoutes([{ path: '/', element: <ScopeContext value={{ kind: 'project', project: defaultProject }}><DraftNavigationGuard><AgentDBConnections agentId={agent.id} configVersion={1} cli="psql" /></DraftNavigationGuard></ScopeContext> }], '/')
}

describe('Project Agent DB connection sets, formal HTTP fixture', () => {
  it('a clean form follows resolved set changes under the same configVersion and does not keep the old automatic account', async () => {
    let data = connections({ accountIds: [a.id], status: 'resolved', connections: [{ accountId: a.id, name: a.name, versionId: a.currentVersionId, status: 'resolved' }] })
    const env = renderDB(() => data)
    await waitFor(() => expect(screen.getAllByRole('checkbox')).toHaveLength(2))
    expect(screen.getAllByRole('checkbox')[0]).toBeChecked()
    data = connections({ credentialsRevision: 2 })
    await act(() => env.client.invalidateQueries({ queryKey: dbConnectionKey(agent.id, 'psql') }))
    await waitFor(() => screen.getAllByRole('checkbox').forEach(box => expect(box).not.toBeChecked()))
  })

  it('without a set it keeps the legacy single editing; after switching to an explicit empty set the legacy write entry is removed', async () => {
    const user = userEvent.setup()
    let data = connections({ mode: 'legacy_single' })
    const selection = emptySelections({ ...agent, clis: [{ name: 'psql', hasTemplate: true, credential: null }] })
    server.use(http.get(apiUrl('/agents/:agentId/credential-selections'), () => HttpResponse.json(selection)),
      http.put(apiUrl('/agents/:agentId/db-connections/:cli'), () => { data = connections(); return HttpResponse.json(data) }))
    renderDB(() => data, [])
    expect(await screen.findByLabelText('psql project account')).toBeInTheDocument()
    await user.click(await screen.findByRole('button', { name: 'Save empty set (grant no connections)' }))
    await waitFor(() => expect(screen.queryByLabelText('psql project account')).toBeNull())
    expect(await screen.findByText('Current mode: Explicit connection set (legacy single-account editing is off) · No connection configured')).toBeInTheDocument()
  })

  it('two candidates in the account store are not granted automatically; one full PUT by stable IDs, nothing written to the legacy single', async () => {
    const user = userEvent.setup(), writes: unknown[] = []
    let data = connections(), singles = 0
    server.use(http.put(apiUrl('/agents/:agentId/db-connections/:cli'), async ({ request }) => {
      writes.push(await request.json()); data = connections({ accountIds: [a.id, b.id], status: 'resolved', connections: [a, b].map(item => ({ accountId: item.id, name: item.name, versionId: item.currentVersionId, status: 'resolved' })) }); return HttpResponse.json(data)
    }), http.put(apiUrl('/agents/:agentId/credential-selections'), () => { singles++; return HttpResponse.json({}) }))
    renderDB(() => data)
    const boxes = await screen.findAllByRole('checkbox')
    expect(boxes).toHaveLength(2); boxes.forEach(box => expect(box).not.toBeChecked())
    expect(screen.queryByRole('combobox')).toBeNull()
    await user.click(boxes[1]); await user.click(boxes[0]); await user.click(screen.getByRole('button', { name: 'Save connection set' }))
    await waitFor(() => expect(writes).toEqual([{ accountIds: [a.id, b.id], expectedConfigVersion: 1 }]))
    expect(singles).toBe(0)
  })

  it('an explicit empty PUT [] and a DELETE back to legacy mode are different requests and delete no account', async () => {
    const user = userEvent.setup(), writes: unknown[] = [], deletes: string[] = []
    let data = connections({ accountIds: [a.id], connections: [{ accountId: a.id, name: a.name, versionId: a.currentVersionId, status: 'resolved' }], status: 'resolved' })
    server.use(http.put(apiUrl('/agents/:agentId/db-connections/:cli'), async ({ request }) => { writes.push(await request.json()); data = connections(); return HttpResponse.json(data) }),
      http.delete(apiUrl('/agents/:agentId/db-connections/:cli'), ({ request }) => { deletes.push(new URL(request.url).search); return HttpResponse.json(data) }))
    renderDB(() => data)
    await user.click((await screen.findAllByRole('checkbox'))[0])
    await user.click(screen.getByRole('button', { name: 'Save empty set (grant no connections)' }))
    await waitFor(() => expect(writes).toEqual([{ accountIds: [], expectedConfigVersion: 1 }]))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Delete the set and switch back to legacy mode' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: 'Delete the set and switch back to legacy mode' }))
    await waitFor(() => expect(deletes).toEqual(['?expectedConfigVersion=1']))
    expect(writes).toHaveLength(1)
  })

  it('412 keeps the set draft and sends only once; a failed read of the latest cannot be adopted; after review it saves with the pinned new version', async () => {
    const user = userEvent.setup(), writes: unknown[] = []
    let readFail = false, newest = false
    server.use(http.put(apiUrl('/agents/:agentId/db-connections/:cli'), async ({ request }) => { writes.push(await request.json()); readFail = true; return HttpResponse.json(errorBody('precondition_failed'), { status: 412 }) }))
    const env = renderDB(() => connections())
    server.use(http.get(apiUrl('/agents/:agentId/db-connections/:cli'), () => readFail ? HttpResponse.json(errorBody('internal_error'), { status: 500 }) : HttpResponse.json(connections({ configVersion: newest ? 2 : 1 }))))
    await user.click((await screen.findAllByRole('checkbox'))[1]); await user.click(screen.getByRole('button', { name: 'Save connection set' }))
    await screen.findByText('The config was updated elsewhere; your changes weren\'t saved')
    expect((screen.getAllByRole('checkbox'))[1]).toBeChecked()
    expect(screen.getByRole('button', { name: 'Reviewed, adopt this version' })).toBeDisabled()
    expect(writes).toHaveLength(1)
    readFail = false; newest = true
    await user.click(screen.getByRole('button', { name: 'Read the latest again' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Reviewed, adopt this version' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: 'Reviewed, adopt this version' }))
    await user.click(screen.getByRole('button', { name: 'Save connection set' }))
    await waitFor(() => expect(writes).toEqual([{ accountIds: [b.id], expectedConfigVersion: 1 }, { accountIds: [b.id], expectedConfigVersion: 2 }]))
    expect(JSON.stringify(env.client.getMutationCache().getAll())).not.toContain('accountIds')
  })

  it('a remote update does not overwrite a dirty set; a deleted member keeps its original ID and fails on its own, candidate B is not picked instead', async () => {
    const user = userEvent.setup()
    let data = connections({ accountIds: [a.id], status: 'account_deleted', connections: [{ accountId: a.id, name: a.name, versionId: null, status: 'account_deleted' }] })
    const env = renderDB(() => data, [b])
    expect(await screen.findByText('Account deleted')).toBeInTheDocument()
    await waitFor(() => expect(screen.getAllByRole('checkbox')).toHaveLength(2))
    const boxes = screen.getAllByRole('checkbox'); expect(boxes[0]).toBeChecked(); expect(boxes[1]).not.toBeChecked()
    await user.click(boxes[1])
    data = connections({ configVersion: 2, accountIds: [] })
    await act(() => env.client.invalidateQueries({ queryKey: dbConnectionKey(agent.id, 'psql') }))
    expect(screen.getAllByRole('checkbox')).toHaveLength(2)
    screen.getAllByRole('checkbox').forEach(box => expect(box).toBeChecked())
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save connection set' })).toBeDisabled())
  })
})

it('platform psql keeps its private single slot and does not query project DB sets', async () => {
  let dbReads = 0
  server.use(http.get(apiUrl('/agents/:agentId/db-connections/:cli'), () => { dbReads++; return HttpResponse.json(connections()) }))
  await renderConfig({ agent: makeAgent({ scope: 'platform', projectId: null, clis: [{ name: 'psql', hasTemplate: true, credential: null }] }), templates: [{ name: 'psql', version: '18.3', fields: [{ name: 'password', label: 'Password', type: 'secret', required: true }], injection: 'files', install: { status: 'not_installed' }, warnings: [] }] })
  expect(await screen.findByRole('button', { name: 'Enter credential' })).toBeInTheDocument()
  expect(screen.queryByText('Granted psql connections')).toBeNull()
  expect(dbReads).toBe(0)
})

it('formal impact marks set members as connection set and does not present them as automatic', async () => {
  server.use(http.get(apiUrl('/projects/:projectId/credential-account-impact'), () => HttpResponse.json({ projectId: defaultProject.id, accountId: a.id, credentialsRevision: 2, agents: [{ agentId: agent.id, name: 'Database assistant', mode: 'connection_set', configVersion: 1 }] })))
  renderRoutes([{ path: '/', element: <AccountImpact projectId={defaultProject.id} purpose="cli" slot="psql" accountId={a.id} /> }], '/')
  expect(await screen.findByText(/Database assistant \(connection set\)/)).toBeInTheDocument()
  expect(screen.queryByText(/Database assistant \(automatic\)/)).toBeNull()
  expect(screen.getByText(/After deletion, explicit choices or connection-set entries for it become invalid/)).toBeInTheDocument()
})

it('a tool that is not ready shows the formal install reason and does not treat a saved account as a working tool or connection', () => {
  renderRoutes([{ path: '/', element: <CliToolStatus template={{ name: 'psql', version: '18.3', fields: [], injection: 'files', install: { status: 'failed', error: 'A required runtime library is missing' }, warnings: [] }} /> }], '/')
  expect(screen.getByText('Tool setup: Install failed · psql v18.3')).toBeInTheDocument()
  expect(screen.getByText('A required runtime library is missing')).toBeInTheDocument()
  expect(screen.getByText('Tool status is separate from account entry, remote connection, and authentication results.')).toBeInTheDocument()
})

it('formal multiline keeps PEM line breaks; private key input is masked and only the mask is drawn', async () => {
  const user = userEvent.setup()
  function Fields() {
    const [values, setValues] = useState<Record<string, string>>({})
    return <CredentialFields fields={[{ name: 'ca', label: 'CA certificate', type: 'string', required: false, multiline: true }, { name: 'key', label: 'Client private key', type: 'secret', required: false, multiline: true }]} values={values} onChange={(name, value) => setValues(previous => ({ ...previous, [name]: value }))} />
  }
  renderRoutes([{ path: '/', element: <Fields /> }], '/')
  await user.type(screen.getByLabelText('CA certificate'), 'CERT{enter}LINE')
  await user.type(screen.getByLabelText('Client private key'), 'PRIVATE{enter}KEY')
  expect(screen.getByLabelText('CA certificate')).toHaveValue('CERT\nLINE')
  expect(screen.getByLabelText('Client private key')).toHaveValue('PRIVATE\nKEY')
  expect(screen.getByLabelText('Client private key')).toHaveClass('text-transparent', 'selection:text-transparent')
  expect(screen.queryByText('PRIVATE')).toBeNull()
})
