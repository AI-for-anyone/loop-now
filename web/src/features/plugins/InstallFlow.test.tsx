import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { apiUrl, errorBody } from '@/test/api'
import { inDesktopShell } from '@/test/desktop'
import { serverShowsDialog } from '@/test/path-picks'
import { makeComponents, makeManifest, makePlugin, makePreview, makeResolvedSource } from '@/test/plugins'
import { keys } from '@/api/queries'
import { renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import { InstallSourceDialog } from './InstallFlow'

const lark = makePreview({
  trusted: false,
  source: makeResolvedSource({ kind: 'git', location: 'https://github.com/foo/loop-now-lark-notify', ref: 'v0.3.1', commit: 'abc1234def' }),
  manifest: makeManifest({
    id: 'lark-notify',
    displayName: 'Lark notifications',
    version: '0.3.1',
    events: ['inbox.received', 'question.asked', 'run.ended'],
    permissions: ['inbox.read', 'inbox.send', 'state'],
    components: makeComponents({ channels: [{ id: 'lark', label: 'Lark', manageIdentities: false }] }),
  }),
})

// The preview and install endpoints: record every request body.
function handlers({ preview = lark, install }: { preview?: typeof lark; install?: (body: Record<string, unknown>) => Response | undefined } = {}) {
  const previews: unknown[] = [], installs: Record<string, unknown>[] = []
  server.use(
    http.get(apiUrl('/plugin-sources/builtin'), () => HttpResponse.json({ sources: [{ id: 'hello', displayName: 'hello example', description: 'A minimal example on each side', version: '1.0.0' }, { id: 'webhook', displayName: 'Webhook notifications', description: 'POSTs when the Inbox gets a new message', version: '1.2.0' }] })),
    http.post(apiUrl('/plugin-previews'), async ({ request }) => {
      previews.push(await request.json())
      return HttpResponse.json(preview, { status: 201 })
    }),
    http.post(apiUrl('/plugins'), async ({ request }) => {
      const body = (await request.json()) as Record<string, unknown>
      installs.push(body)
      return install?.(body) ?? HttpResponse.json(makePlugin({ id: preview.manifest.id, displayName: preview.manifest.displayName }), { status: 201 })
    }),
  )
  return { previews, installs }
}

function renderInstall() {
  const onOpenChange = vi.fn(), onInstalled = vi.fn()
  const { client } = renderWithQuery(<InstallSourceDialog open onOpenChange={onOpenChange} installed={['webhook']} onInstalled={onInstalled} />)
  return { onOpenChange, onInstalled, client }
}

describe('Installing a plugin: read manifest → confirm → install', () => {
  it('git source: after reading the manifest, shows components, events and permissions with explanations and the unreviewed warning; installs with confirm after confirming', async () => {
    const user = userEvent.setup()
    const { previews, installs } = handlers()
    const { onInstalled } = renderInstall()
    await user.click(screen.getByRole('radio', { name: 'git repository' }))
    await user.type(screen.getByLabelText('Repository URL'), 'https://github.com/foo/loop-now-lark-notify')
    await user.type(screen.getByLabelText('Branch, tag, or commit (optional)'), 'v0.3.1')
    await user.click(screen.getByRole('button', { name: 'Read manifest' }))
    const dialog = await screen.findByRole('dialog', { name: /Install Lark notifications/ })
    expect(previews).toEqual([{ source: { kind: 'git', location: 'https://github.com/foo/loop-now-lark-notify', ref: 'v0.3.1' } }])
    expect(within(dialog).getByRole('note')).toHaveTextContent('From git, not in any market, and nobody has reviewed it.')
    expect(within(dialog).getByRole('note')).toHaveTextContent('https://github.com/foo/loop-now-lark-notify @ v0.3.1 (abc1234)')
    expect(within(dialog).getByText('question.asked').parentElement).toHaveTextContent('question.asked · An Agent asked you a question')
    expect(within(dialog).getByText('inbox.send').parentElement).toHaveTextContent('inbox.send · Send messages as the plugin')
    expect(within(dialog).getByText('Channels').parentElement).toHaveTextContent('Channels · Lark')
    expect(within(dialog).getAllByText('None')[0].parentElement).toHaveTextContent('Doesn\'t install anything into Agents\' Pi')
    await user.click(within(dialog).getByRole('button', { name: 'I\'ve reviewed it, install' }))
    await waitFor(() => expect(installs).toEqual([{ previewId: lark.previewId, confirm: true }]))
    await waitFor(() => expect(onInstalled).toHaveBeenCalledWith(expect.objectContaining({ id: 'lark-notify' })))
    expect(screen.queryByRole('dialog', { name: /Install Lark notifications/ })).toBeNull()
  })

  it('official source: installed ones cannot be chosen again; a trusted one installs directly without confirm', async () => {
    const user = userEvent.setup()
    const hello = makePreview({ manifest: makeManifest({ id: 'hello', displayName: 'hello example', version: '1.0.0', components: makeComponents({ agentExtensions: 1, skills: ['skills/hello'] }) }) })
    const { previews, installs } = handlers({ preview: hello })
    renderInstall()
    expect(screen.getByRole('radio', { name: 'git repository' })).toBeChecked()
    await user.click(screen.getByRole('radio', { name: 'Official plugin' }))
    expect(await screen.findByRole('radio', { name: /Webhook notifications/ })).toBeDisabled()
    await user.click(screen.getByRole('radio', { name: /hello example/ }))
    await user.click(screen.getByRole('button', { name: 'Read manifest' }))
    const dialog = await screen.findByRole('dialog', { name: /Install hello example/ })
    expect(previews).toEqual([{ source: { kind: 'builtin', location: 'hello' } }])
    expect(within(dialog).queryByRole('note')).toBeNull()
    // The model providers registered by the extension are read only after the install is confirmed (reading them runs plugin code).
    expect(within(dialog).getByText('Pi extension, 1 file').parentElement).toHaveTextContent('Pi extension, 1 file · Loaded when a granted Agent starts; the model providers it registers are read after installing')
    await user.click(within(dialog).getByRole('button', { name: 'Install' }))
    await waitFor(() => expect(installs).toEqual([{ previewId: hello.previewId }]))
  })

  it('after installing, the provider catalog and model lists are invalidated and read again when next needed', async () => {
    const user = userEvent.setup()
    handlers({ preview: makePreview({ manifest: makeManifest({ id: 'hello', displayName: 'hello example', components: makeComponents({ agentExtensions: 1 }) }) }) })
    const { client, onInstalled } = renderInstall()
    client.setQueryData(keys.modelProviders, { piVersion: '1.4.0', catalogGeneratedAt: null, providers: [] })
    client.setQueryData(keys.agentModelProviders('agent-1'), { piVersion: '1.4.0', catalogGeneratedAt: null, providers: [] })
    client.setQueryData(keys.models('grok-cli'), { providerId: 'grok-cli', models: [] })
    await user.click(screen.getByRole('radio', { name: 'Official plugin' }))
    await user.click(await screen.findByRole('radio', { name: /hello example/ }))
    await user.click(screen.getByRole('button', { name: 'Read manifest' }))
    const dialog = await screen.findByRole('dialog', { name: /Install hello example/ })
    expect(client.getQueryState(keys.modelProviders)?.isInvalidated).toBe(false)
    await user.click(within(dialog).getByRole('button', { name: 'Install' }))
    await waitFor(() => expect(onInstalled).toHaveBeenCalled())
    expect(client.getQueryState(keys.modelProviders)?.isInvalidated).toBe(true)
    expect(client.getQueryState(keys.agentModelProviders('agent-1'))?.isInvalidated).toBe(true)
    expect(client.getQueryState(keys.models('grok-cli'))?.isInvalidated).toBe(true)
  })

  it('409 name_conflict: explains that the plugin id is already installed or that a model provider it registers has the same id as one from another plugin, and adds the server message', async () => {
    const user = userEvent.setup()
    handlers({
      preview: makePreview({ manifest: makeManifest({ id: 'grok', displayName: 'Grok subscription', components: makeComponents({ agentExtensions: 1 }) }) }),
      install: () => HttpResponse.json(errorBody('name_conflict', 'Plugin grok-old already registers model provider grok-cli, so plugin grok cannot register it'), { status: 409 }),
    })
    renderInstall()
    await user.click(screen.getByRole('radio', { name: 'git repository' }))
    await user.type(screen.getByLabelText('Repository URL'), 'https://example.com/grok.git')
    await user.click(screen.getByRole('button', { name: 'Read manifest' }))
    const dialog = await screen.findByRole('dialog', { name: /Install Grok subscription/ })
    await user.click(within(dialog).getByRole('button', { name: 'Install' }))
    expect(await within(dialog).findByText('Not installed: a plugin with this id is already installed, or a model provider it registers has the same id as one from another installed plugin: Plugin grok-old already registers model provider grok-cli, so plugin grok cannot register it')).toBeInTheDocument()
    expect(within(dialog).queryByText(/That name is already taken/)).toBeNull()
  })

  it('the confirmation shows the preview README below the manifest (read in the console language) and notes when it is truncated', async () => {
    const user = userEvent.setup()
    const hello = makePreview({ manifest: makeManifest({ id: 'hello', displayName: 'hello example', version: '1.0.0' }) })
    handlers({ preview: hello })
    const reads: string[] = []
    server.use(
      http.get(apiUrl('/plugin-previews/:previewId/readme'), ({ request }) => {
        reads.push(request.url.replace(/^.*\/api\/v1/, ''))
        return HttpResponse.json({ file: 'README.en-US.md', markdown: '## Usage\n\nAfter granting, type `/hello` in the chat.', truncated: true })
      }),
    )
    renderInstall()
    await user.click(screen.getByRole('radio', { name: 'Official plugin' }))
    await user.click(await screen.findByRole('radio', { name: /hello example/ }))
    await user.click(screen.getByRole('button', { name: 'Read manifest' }))
    const dialog = await screen.findByRole('dialog', { name: /Install hello example/ })
    const readme = await within(dialog).findByRole('region', { name: 'About' })
    expect(reads).toEqual([`/plugin-previews/${hello.previewId}/readme?locale=en-US`])
    expect(readme).toHaveTextContent('README.en-US.md')
    expect(within(readme).getByRole('heading', { name: 'Usage' })).toBeInTheDocument()
    expect(within(readme).getByText('/hello').tagName).toBe('CODE')
    expect(readme).toHaveTextContent('This README is too long; only the beginning is shown.')
    expect(within(dialog).getByText('Permissions it asks for').compareDocumentPosition(readme) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('a local folder needs an absolute path; when reading fails, it stays on the source step and shows the reason', async () => {
    const user = userEvent.setup()
    handlers()
    server.use(http.post(apiUrl('/plugin-previews'), () => HttpResponse.json(errorBody('plugin_source_failed', 'no package.json'), { status: 422 })))
    const { onOpenChange } = renderInstall()
    await user.click(screen.getByRole('radio', { name: 'Local folder' }))
    await user.type(screen.getByLabelText('Absolute path to a folder on the server machine'), 'plugins/mine')
    await user.click(screen.getByRole('button', { name: 'Read manifest' }))
    expect(screen.getByText('Enter an absolute path')).toBeInTheDocument()
    await user.clear(screen.getByLabelText('Absolute path to a folder on the server machine'))
    await user.type(screen.getByLabelText('Absolute path to a folder on the server machine'), '/Users/me/plugins/mine')
    await user.click(screen.getByRole('button', { name: 'Read manifest' }))
    expect(await screen.findByText(/Couldn't fetch the plugin source, or installing its dependencies failed/)).toBeInTheDocument()
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
  })

  it('in the desktop window, a local folder can be chosen with the system folder dialog; a git repository has no such button', async () => {
    const pickPath = inDesktopShell({ answer: '/Users/me/plugins/mine' })
    const user = userEvent.setup()
    const { previews } = handlers()
    renderInstall()
    await user.click(screen.getByRole('radio', { name: 'git repository' }))
    expect(screen.queryByRole('button', { name: 'Choose…' })).toBeNull()
    await user.click(screen.getByRole('radio', { name: 'Local folder' }))
    await user.click(screen.getByRole('button', { name: 'Choose…' }))
    expect(pickPath).toHaveBeenCalledWith({ kind: 'directory' })
    await waitFor(() => expect(screen.getByLabelText('Absolute path to a folder on the server machine')).toHaveValue('/Users/me/plugins/mine'))
    await user.click(screen.getByRole('button', { name: 'Read manifest' }))
    await waitFor(() => expect(previews).toEqual([{ source: { kind: 'local', location: '/Users/me/plugins/mine' } }]))
  })

  it('in a browser whose server can show this computer\'s dialog, a local folder can be chosen there', async () => {
    const picks = serverShowsDialog({ path: '/Users/me/plugins/mine' })
    const user = userEvent.setup()
    const { previews } = handlers()
    renderInstall()
    await user.click(screen.getByRole('radio', { name: 'Local folder' }))
    await user.click(await screen.findByRole('button', { name: 'Choose…' }))
    await waitFor(() => expect(screen.getByLabelText('Absolute path to a folder on the server machine')).toHaveValue('/Users/me/plugins/mine'))
    expect(picks.requests).toEqual([{ kind: 'directory' }])
    await user.click(screen.getByRole('button', { name: 'Read manifest' }))
    await waitFor(() => expect(previews).toEqual([{ source: { kind: 'local', location: '/Users/me/plugins/mine' } }]))
  })

  it('in a browser whose server cannot show the dialog, a local folder is typed: there is no “Choose…”', async () => {
    const user = userEvent.setup()
    handlers()
    renderInstall()
    await user.click(screen.getByRole('radio', { name: 'Local folder' }))
    expect(screen.getByLabelText('Absolute path to a folder on the server machine')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Choose…' })).toBeNull()
  })

  it('when the server still asks for confirmation (409 plugin_confirmation_required), adds the warning, and clicking again installs with confirm', async () => {
    const user = userEvent.setup()
    const trusted = makePreview()
    let first = true
    const { installs } = handlers({
      preview: trusted,
      install: () => {
        if (!first) return undefined
        first = false
        return HttpResponse.json(errorBody('plugin_confirmation_required', 'confirm required'), { status: 409 })
      },
    })
    renderInstall()
    await user.click(screen.getByRole('radio', { name: 'git repository' }))
    await user.type(screen.getByLabelText('Repository URL'), 'https://example.com/webhook.git')
    await user.click(screen.getByRole('button', { name: 'Read manifest' }))
    const dialog = await screen.findByRole('dialog', { name: /Install Webhook notifications/ })
    await user.click(within(dialog).getByRole('button', { name: 'Install' }))
    expect(await within(dialog).findByRole('note')).toHaveTextContent('nobody has reviewed it')
    await user.click(within(dialog).getByRole('button', { name: 'I\'ve reviewed it, install' }))
    await waitFor(() => expect(installs).toEqual([{ previewId: trusted.previewId }, { previewId: trusted.previewId, confirm: true }]))
  })
})
