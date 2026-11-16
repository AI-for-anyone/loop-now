import userEvent from '@testing-library/user-event'
import { act, screen, waitFor } from '@testing-library/react'
import { focusManager } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { apiUrl, defaultProject, errorBody, unknownEndpointBody } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { providers } from '@/test/config'
import { credentialKeys } from '@/api/project-credentials'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { ProjectCredentialSettings } from './ProjectAccounts'

function renderSettings() {
  return renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectCredentialSettings projectId={defaultProject.id} /></DraftNavigationGuard> }], '/')
}

describe('Project credentials with an old backend', () => {
  it('an exact missing route shows one combined notice and queries no impact or operation; reading again on focus recovers automatically with a new backend', async () => {
    let supported = false, lists = 0, ancillary = 0, writes = 0
    server.use(
      http.get(apiUrl('/projects/:projectId/credential-accounts'), () => {
        lists++
        return supported ? HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 0, accounts: [] }) : HttpResponse.json(unknownEndpointBody(), { status: 404 })
      }),
      http.get(apiUrl('/model-providers'), () => HttpResponse.json(providers)),
      http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [] })),
      http.get(apiUrl('/projects/:projectId/credential-account-impact'), () => { ancillary++; return HttpResponse.json({}) }),
      http.get(apiUrl('/projects/:projectId/model-auth/operations'), () => { ancillary++; return HttpResponse.json({}) }),
      http.post(apiUrl('/projects/:projectId/credential-accounts'), () => { writes++; return HttpResponse.json({}) }),
    )
    renderSettings()
    await screen.findByText(/This server doesn't support project credentials yet/)
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(screen.getByRole('button', { name: 'Browser login' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Add account' })).toBeDisabled()
    expect(screen.queryByText('No accounts yet')).toBeNull()
    expect(screen.queryByLabelText('Add model account · provider')).toBeNull()
    expect(ancillary).toBe(0); expect(writes).toBe(0)
    supported = true
    await act(async () => { focusManager.setFocused(false); focusManager.setFocused(true) })
    await screen.findByLabelText('Add model account · provider')
    expect(screen.queryByText(/This server doesn't support project credentials yet/)).toBeNull()
    expect(screen.getByLabelText('Add model account · provider')).toBeEnabled()
    expect(screen.getAllByText('No accounts yet')).toHaveLength(2)
    expect(lists).toBeGreaterThanOrEqual(2)
    expect(ancillary).toBe(0); expect(writes).toBe(0)
    focusManager.setFocused(undefined)
  })

  it('a real resource 404 is still a resource error, not inferred as unsupported or as no accounts; an existing draft is hidden for a while and then restored', async () => {
    const user = userEvent.setup()
    let gone = false
    server.use(
      http.get(apiUrl('/projects/:projectId/credential-accounts'), () => gone ? HttpResponse.json(errorBody('not_found', 'Project not found'), { status: 404 }) : HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 0, accounts: [] })),
      http.get(apiUrl('/model-providers'), () => HttpResponse.json(providers)),
      http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [] })),
    )
    server.use(http.get(apiUrl('/projects/:projectId/credential-account-impact'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 0, accountId: null, agents: [] })))
    const { client } = renderSettings()
    await screen.findByLabelText('Add model account · provider')
    await user.click(screen.getByLabelText('Add model account · provider'))
    await user.click(await screen.findByRole('option', { name: /^Anthropic/ }))
    await user.type(screen.getByLabelText('API Key'), 'local-fixture-draft')
    gone = true
    await act(() => client.invalidateQueries({ queryKey: credentialKeys.project(defaultProject.id) }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Not found, or already deleted'))
    expect(screen.queryByText(/This server doesn't support project credentials yet/)).toBeNull()
    expect(screen.queryByRole('combobox')).toBeNull()
    expect(screen.getByRole('button', { name: 'Browser login' })).toBeDisabled()
    gone = false
    await act(() => client.invalidateQueries({ queryKey: credentialKeys.project(defaultProject.id) }))
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
    expect(screen.getByLabelText('API Key')).toBeVisible()
    expect(screen.getByLabelText('API Key')).toHaveValue('local-fixture-draft')
  })
})
