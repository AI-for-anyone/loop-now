import { act, screen, waitFor, within, cleanup } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { agentsUrl, apiUrl, defaultAgentsPath, errorBody, makeAgent, makeRun, toListItem } from '@/test/api'
import { credentialMeta, larkTemplate, renderConfig } from '@/test/config'
import { FakeIntersectionObserver } from '@/test/intersection-observer'
import { modelOptions } from '@/test/model-picker'

const region = (name: string) => screen.getByRole('region', { name })

describe('Layout', () => {
  it('"The current reply uses the config from before the change" sits in the same sticky note as the effect notice', async () => {
    await renderConfig({
      agent: makeAgent({ configVersion: 3 }),
      status: { busy: true, currentRun: makeRun({ status: 'running', configVersion: 2 }) },
    })
    const stale = await screen.findByText('The current reply uses the config from before the change.')
    expect(stale.closest('[role="note"]')).toHaveTextContent('Changes take effect from the next dispatched message; a reply in progress is not affected.')
  })

  it('copies the internal ID and shows Copied on success', async () => {
    const { agent, user } = await renderConfig()
    const basic = region('Basics')
    await user.click(within(basic).getByRole('button', { name: 'Copy' }))
    await expect(navigator.clipboard.readText()).resolves.toBe(agent.id)
    expect(within(basic).getByRole('button', { name: 'Copied' })).toBeInTheDocument()
  })

  // jsdom does no layout, so we can only confirm that the scroll area is a positioning
  // container; that the whole page does not get stretched taller is checked in a browser.
  it('the page scroll area is a positioning container, so screen-reader text in sections does not stretch the whole page', async () => {
    await renderConfig()
    expect(region('Basics').closest('.overflow-y-auto')).toHaveClass('relative')
  })
})

// "Printed sheet" redesign, step 2: numbered sections, yellow sticky notes and the section ruler in the stub.
describe('Redesigned layout', () => {
  const sections = ['Basics', 'Prompt', 'Local directories', 'Model account credential', 'Skills', 'Available CLIs', 'Plugin grants', 'Delete Agent']

  it('the eight sections are numbered 01–08 in order: the number is a hollow decoration and the title gets a yellow marker stroke; dashed lines separate the sections, with none above the first', async () => {
    await renderConfig()
    sections.forEach((name, index) => {
      const section = region(name)
      const number = section.querySelector('[data-section-number]')!
      expect(number).toHaveTextContent(String(index + 1).padStart(2, '0'))
      expect(number).toHaveAttribute('aria-hidden', 'true')
      expect(number).toHaveClass('max-nav:hidden', 'text-transparent')
      expect(within(section).getByRole('heading', { level: 2, name })).toHaveClass('riso-marker', 'font-display-cn')
      if (index === 0) expect(section).not.toHaveClass('border-t-2')
      else expect(section).toHaveClass('border-t-2', 'border-dashed')
    })
  })

  it('sections are no longer separate cards but sit directly on the paper; the Delete Agent section stays red', async () => {
    await renderConfig()
    expect(region('Basics')).not.toHaveClass('shadow-print-rest')
    expect(region('Basics')).not.toHaveClass('bg-paper-raised')
    const danger = region('Delete Agent')
    expect(within(danger).getByRole('heading', { level: 2, name: 'Delete Agent' })).toHaveClass('text-red-text')
    expect(danger.querySelector('[data-section-number]')).toHaveClass('[-webkit-text-stroke:1.6px_var(--red-text)]')
    expect(danger.querySelector('[data-section-body]')).toHaveClass('riso-stripes-red', 'bg-error-field')
  })

  it('the internal ID sits in a dashed box, and the copy button is a small yellow square with a navy outline', async () => {
    const { agent } = await renderConfig()
    const id = within(region('Basics')).getByText(agent.id)
    expect(id.parentElement).toHaveClass('border-dashed', 'rounded-input')
    expect(within(region('Basics')).getByRole('button', { name: 'Copy' })).toHaveClass('border-2', 'border-ink', 'bg-riso-yellow')
  })

  // jsdom does no layout: positions are confirmed with a browser check (the note under “模型提供方”
  // (Model provider) in the left column no longer pushes “模型” (Model) in the right column down).
  it('two fields in the same row align to the top: a note under one grows downward without pushing the other column down', async () => {
    await renderConfig()
    const basic = region('Basics')
    // Internal ID and display name share a row.
    expect(within(basic).getByLabelText('Display name').closest('.grid')!.parentElement).toHaveClass('items-start')
    // Model provider and model share a row (the grid inside the model picker).
    expect(within(basic).getByLabelText('Model provider').closest('.grid')!.parentElement).toHaveClass('items-start')
  })

  it('the body column and the contents are both inside the paper and use its thin scrollbar, without hiding scrollbars (the contents ruler is not a scroll position)', async () => {
    await renderConfig()
    const sheet = region('Basics').closest('[data-sheet]')!
    expect(sheet).toHaveClass('riso-scrollbar')
    const body = region('Basics').closest('.overflow-y-auto')!
    const toc = screen.getByRole('navigation', { name: 'Contents' })
    for (const area of [body, toc]) {
      expect(sheet).toContainElement(area as HTMLElement)
      expect(area.className).not.toMatch(/no-scrollbar/)
    }
  })

  it('the "takes effect from the next message" notice is a yellow sticky note', async () => {
    await renderConfig()
    const note = screen.getByText('Changes take effect from the next dispatched message; a reply in progress is not affected.').closest('[role="note"]')!
    expect(note).toHaveClass('riso-tilt', '[--tilt-bg:var(--riso-yellow)]')
    expect(note.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })

  it('the contents sit in the stub on the right of the paper: a row of ticks along the right edge, each entry is "name number short tick", and the current entry is marked with a yellow marker and a pink arrow', async () => {
    await renderConfig()
    const toc = screen.getByRole('navigation', { name: 'Contents' })
    expect(toc).toHaveClass('border-l-2', 'border-dashed', 'max-nav:hidden')
    expect(within(toc).getByText('Contents')).toHaveAttribute('aria-hidden', 'true')
    // A row of ticks along the right edge (decoration).
    expect(toc.querySelector('ol > div[aria-hidden="true"]')).toHaveClass('absolute', 'right-0', 'w-3')
    expect(toc.querySelector('[data-ruler-mark]')).toBeNull()
    act(() => FakeIntersectionObserver.trigger(region('Skills'), true))
    const current = within(toc).getByRole('link', { name: 'Skills' })
    expect(current.closest('li')!.querySelector('[data-ruler-mark]')).not.toBeNull()
    expect(toc.querySelectorAll('[data-ruler-mark]')).toHaveLength(1)
    // The Delete Agent entry is red.
    expect(within(toc).getByRole('link', { name: 'Delete Agent' })).toHaveClass('text-red-text')
  })
})

describe('Section contents', () => {
  const toc = () => screen.getByRole('navigation', { name: 'Contents' })
  const tocLink = (name: string) => within(toc()).getByRole('link', { name })
  const highlighted = () => within(toc()).getAllByRole('link').filter((link) => link.getAttribute('aria-current') === 'location')

  it('lists the eight sections in order; clicking an entry jumps to its section and focuses the section title', async () => {
    const scroll = vi.spyOn(Element.prototype, 'scrollIntoView')
    try {
      const { user, router } = await renderConfig()
      expect(within(toc()).getAllByRole('link').map((link) => link.textContent)).toEqual([
        '01Basics',
        '02Prompt',
        '03Local directories',
        '04Model account credential',
        '05Skills',
        '06Available CLIs',
        '07Plugin grants',
        '08Delete Agent',
      ])
      await user.click(tocLink('Available CLIs'))
      const clis = region('Available CLIs')
      expect(within(clis).getByRole('heading', { level: 2, name: 'Available CLIs' })).toHaveFocus()
      expect(scroll.mock.contexts).toContain(clis)
      // The URL does not change.
      expect(router.state.location.pathname).toMatch(/\/config$/)
      expect(router.state.location.hash).toBe('')

      await user.click(tocLink('Delete Agent'))
      expect(within(region('Delete Agent')).getByRole('heading', { level: 2, name: 'Delete Agent' })).toHaveFocus()
    } finally {
      scroll.mockRestore()
    }
  })

  it('highlights the visible section, taking the earliest one when several are visible', async () => {
    await renderConfig()
    expect(highlighted()).toHaveLength(0)
    act(() => FakeIntersectionObserver.trigger(region('Available CLIs'), true))
    expect(highlighted()).toEqual([tocLink('Available CLIs')])
    act(() => FakeIntersectionObserver.trigger(region('Skills'), true))
    expect(highlighted()).toEqual([tocLink('Skills')])
    act(() => FakeIntersectionObserver.trigger(region('Skills'), false))
    expect(highlighted()).toEqual([tocLink('Available CLIs')])
    // When all of them have left the viewport, the last highlight is kept.
    act(() => FakeIntersectionObserver.trigger(region('Available CLIs'), false))
    expect(highlighted()).toEqual([tocLink('Available CLIs')])
  })

  it('without IntersectionObserver support nothing is highlighted, but jumping still works', async () => {
    vi.stubGlobal('IntersectionObserver', undefined)
    try {
      const { user } = await renderConfig()
      await user.click(tocLink('Available CLIs'))
      expect(within(region('Available CLIs')).getByRole('heading', { level: 2, name: 'Available CLIs' })).toHaveFocus()
      expect(highlighted()).toHaveLength(0)
    } finally {
      vi.unstubAllGlobals()
    }
  })
})

describe('Name, model and Prompt', () => {
  it("shows the read-only internal ID; the provider is chosen first and the model options list only that provider's models", async () => {
    const { agent, user } = await renderConfig()
    const basic = region('Basics')
    expect(within(basic).getByText(agent.id)).toBeInTheDocument()
    const provider = within(basic).getByLabelText('Model provider')
    await waitFor(() => expect(provider).toHaveValue('anthropic'))
    await user.selectOptions(provider, 'openai')
    await waitFor(() => expect(within(basic).getByRole('combobox', { name: 'Model' })).toBeEnabled())
    expect(await modelOptions(user, basic)).toEqual(['GPT X (gpt-x)'])
  })

  it('marks the current model when it is not in the catalog', async () => {
    await renderConfig({ agent: makeAgent({ model: { provider: 'anthropic', modelId: 'retired-model' } }) })
    const basic = region('Basics')
    expect(await within(basic).findByText('The current model isn\'t in the server\'s model catalog')).toBeInTheDocument()
    expect(within(basic).getByText('anthropic / retired-model')).toBeInTheDocument()
  })

  it('asks for confirmation before leaving with an unsaved Prompt', async () => {
    const { user, router } = await renderConfig()
    await user.type(screen.getByLabelText('Agent prompt'), 'New prompt')
    await user.click(screen.getByRole('link', { name: 'Chat' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('You have unsaved changes')
    expect(router.state.location.pathname).toMatch(/\/config$/)
    await user.click(within(dialog).getByRole('button', { name: 'Leave' }))
    await waitFor(() => expect(router.state.location.pathname).toMatch(/\/chat$/))
  })

  it('a duplicate name shows the reason next to the name', async () => {
    const { agent, user } = await renderConfig({
      handlers: [http.patch(apiUrl('/agents/:agentId'), () => HttpResponse.json(errorBody('name_conflict', 'dup'), { status: 409 }))],
    })
    const name = screen.getByLabelText('Display name')
    await user.clear(name)
    await user.type(name, 'Duplicate name')
    await user.click(within(region('Basics')).getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('The project already has an Agent with this name. Pick another.')).toBeInTheDocument()
    expect(name).toHaveValue('Duplicate name')
    void agent
  })
})

describe('Name rules', () => {
  it('a name that breaks the naming rules is reported locally and not submitted', async () => {
    let calls = 0
    const agent = makeAgent()
    const { user } = await renderConfig({
      agent,
      handlers: [
        http.patch(apiUrl(`/agents/${agent.id}`), () => {
          calls += 1
          return HttpResponse.json(agent)
        }),
      ],
    })
    const name = screen.getByLabelText('Display name')
    await user.clear(name)
    await user.type(name, '@dev')
    await user.click(within(region('Basics')).getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Names can\'t start with "@"')).toBeInTheDocument()
    expect(name).toHaveValue('@dev')
    expect(calls).toBe(0)
  })
})

describe('Config effect notice', () => {
  it('the notice is always shown; saving during a reply says the current reply is not affected', async () => {
    let saved: unknown = null
    const agent = makeAgent()
    let current = agent
    const { user } = await renderConfig({
      agent,
      status: { busy: true, currentRun: makeRun({ status: 'running', configVersion: 1 }) },
      handlers: [
        http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
        http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
          saved = await request.json()
          current = { ...agent, name: 'New name', configVersion: 2 }
          return HttpResponse.json(current)
        }),
      ],
    })
    expect(screen.getByText('Changes take effect from the next dispatched message; a reply in progress is not affected.')).toBeInTheDocument()
    await user.clear(screen.getByLabelText('Display name'))
    await user.type(screen.getByLabelText('Display name'), 'New name')
    await user.click(within(region('Basics')).getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Saved. The current reply is not affected; changes take effect from the next message.')).toBeInTheDocument()
    expect(saved).toEqual({ name: 'New name' })
  })

  it('a separate notice shows when the current reply uses the config from before the change', async () => {
    await renderConfig({
      agent: makeAgent({ configVersion: 3 }),
      status: { busy: true, currentRun: makeRun({ status: 'running', configVersion: 2 }) },
    })
    expect(await screen.findByText('The current reply uses the config from before the change.')).toBeInTheDocument()
  })
})

describe("The Agent's Skill copies", () => {
  const template: Schemas['SkillTemplateSummary'] = {
    id: 'tpl-1',
    name: 'pdf-tools',
    description: 'Works with PDFs',
    fileCount: 2,
    createdAt: '2026-10-12T00:00:00Z',
    updatedAt: '2026-10-12T00:00:00Z',
  }
  const copy = (overrides: Partial<Schemas['AgentSkillSummary']> = {}): Schemas['AgentSkillSummary'] => ({
    id: 'as-1',
    name: 'pdf-tools',
    description: 'Works with PDFs',
    sourceTemplateId: 'tpl-1',
    fileCount: 2,
    copiedAt: '2026-10-12T00:00:00Z',
    updatedAt: '2026-10-12T00:00:00Z',
    ...overrides,
  })

  it('adds a template from the library', async () => {
    let copied: Schemas['AgentSkillSummary'][] = []
    const agent = makeAgent()
    const { user } = await renderConfig({
      agent,
      library: [template],
      handlers: [
        http.get(apiUrl(`/agents/${agent.id}/skills`), () => HttpResponse.json({ skills: copied })),
        http.post(apiUrl(`/agents/${agent.id}/skills`), () => {
          copied = [copy()]
          return HttpResponse.json({ ...copy(), files: [], skillMd: '' }, { status: 201 })
        }),
      ],
    })
    await user.click(within(region('Skills')).getByRole('button', { name: 'Add from library' }))
    await user.click(within(await screen.findByRole('listitem', { name: 'pdf-tools' })).getByRole('button', { name: 'Add' }))
    const item = await within(region('Skills')).findByRole('listitem', { name: 'pdf-tools' })
    expect(item).toHaveTextContent('Source template: pdf-tools')
  })

  it('adding a Skill with a name already in use says to remove the existing one first', async () => {
    const agent = makeAgent()
    const { user } = await renderConfig({
      agent,
      library: [template],
      agentSkills: [copy()],
      handlers: [http.post(apiUrl(`/agents/${agent.id}/skills`), () => HttpResponse.json(errorBody('name_conflict', 'dup'), { status: 409 }))],
    })
    await user.click(within(region('Skills')).getByRole('button', { name: 'Add from library' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(within(dialog).getByRole('listitem', { name: 'pdf-tools' })).getByRole('button', { name: 'Add' }))
    expect(await within(dialog).findByText('This Agent already has a Skill with this name. Remove it first, then add.')).toBeInTheDocument()
  })

  it('asks before recopying over the Skill and says edits to SKILL.md will be overwritten', async () => {
    let recopied = 0
    const agent = makeAgent()
    const { user } = await renderConfig({
      agent,
      library: [template],
      agentSkills: [copy()],
      handlers: [
        http.post(apiUrl(`/agents/${agent.id}/skills/as-1/recopy`), () => {
          recopied += 1
          return HttpResponse.json({ ...copy(), files: [], skillMd: '' })
        }),
      ],
    })
    await user.click(await within(region('Skills')).findByRole('button', { name: 'Copy the latest from the library' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('including your edits to SKILL.md')
    expect(recopied).toBe(0)
    await user.click(within(dialog).getByRole('button', { name: 'Copy the latest from the library' }))
    await waitFor(() => expect(recopied).toBe(1))
  })

  it('when the source template is deleted, recopying is unavailable while editing and removing still work', async () => {
    await renderConfig({ agentSkills: [copy({ sourceTemplateId: null })] })
    const item = await within(region('Skills')).findByRole('listitem', { name: 'pdf-tools' })
    expect(within(item).getByRole('button', { name: 'Copy the latest from the library' })).toBeDisabled()
    expect(within(item).getAllByText(/Source template deleted/).length).toBeGreaterThan(0)
    expect(within(item).getByRole('button', { name: 'Edit SKILL.md' })).toBeEnabled()
    expect(within(item).getByRole('button', { name: 'Remove' })).toBeEnabled()
  })

  it("removing a Skill from the Agent deletes the Agent's own copy (including an edited SKILL.md), so the confirmation carries the \"Irreversible\" stamp", async () => {
    const { user } = await renderConfig({ agentSkills: [copy()] })
    const item = await within(region('Skills')).findByRole('listitem', { name: 'pdf-tools' })
    await user.click(within(item).getByRole('button', { name: 'Remove' }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Remove Skill "pdf-tools" from the Agent?' })
    expect(dialog.querySelector('[data-dialog-tab]')).toHaveTextContent('Danger')
    expect(within(dialog).getByText('Irreversible')).toBeInTheDocument()
  })

  it("edits the copy's SKILL.md online", async () => {
    let content: unknown = null
    const agent = makeAgent()
    const { user } = await renderConfig({
      agent,
      agentSkills: [copy()],
      handlers: [
        http.get(apiUrl(`/agents/${agent.id}/skills/as-1`), () => HttpResponse.json({ ...copy(), files: [], skillMd: '---\nname: pdf-tools\n---\nOld' })),
        http.put(apiUrl(`/agents/${agent.id}/skills/as-1/skill-md`), async ({ request }) => {
          content = await request.json()
          return HttpResponse.json({ ...copy(), files: [], skillMd: '' })
        }),
      ],
    })
    await user.click(await within(region('Skills')).findByRole('button', { name: 'Edit SKILL.md' }))
    const editor = await screen.findByLabelText('SKILL.md')
    await waitFor(() => expect(editor).toHaveValue('---\nname: pdf-tools\n---\nOld'))
    await user.type(editor, ' new')
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(content).toEqual({ content: '---\nname: pdf-tools\n---\nOld new' }))
  })
})

describe('Available CLIs', () => {
  it('adding a CLI without a template: marker only, no credential entry', async () => {
    let added: string | null = null
    const agent = makeAgent()
    let current = agent
    const { user } = await renderConfig({
      agent,
      handlers: [
        http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
        http.put(apiUrl(`/agents/${agent.id}/clis/:cli`), ({ params }) => {
          added = params.cli as string
          current = { ...agent, clis: [{ name: 'jq', hasTemplate: false, credential: null }] }
          return HttpResponse.json(current.clis[0])
        }),
      ],
    })
    const clis = region('Available CLIs')
    await user.type(within(clis).getByLabelText('CLI name'), 'jq')
    await user.click(within(clis).getByRole('button', { name: 'Add CLI' }))
    await waitFor(() => expect(added).toBe('jq'))
    const item = await within(clis).findByRole('listitem', { name: 'jq' })
    expect(item).toHaveTextContent('Marker only: not installed, no credential injected')
    expect(within(item).queryByRole('button', { name: /credential/i })).toBeNull()
  })

  it('a network error keeps the input and says the server cannot be reached', async () => {
    const agent = makeAgent()
    const { user } = await renderConfig({ agent, handlers: [http.put(apiUrl(`/agents/${agent.id}/clis/:cli`), () => HttpResponse.error())] })
    const clis = region('Available CLIs')
    await user.type(within(clis).getByLabelText('CLI name'), 'gh')
    await user.click(within(clis).getByRole('button', { name: 'Add CLI' }))
    expect(await within(clis).findByText('Can\'t reach the server')).toBeInTheDocument()
    expect(within(clis).getByLabelText('CLI name')).toHaveValue('gh')
  })

  it('an invalid name is not submitted', async () => {
    const { user } = await renderConfig()
    const clis = region('Available CLIs')
    await user.type(within(clis).getByLabelText('CLI name'), 'Bad Name')
    await user.click(within(clis).getByRole('button', { name: 'Add CLI' }))
    expect(await within(clis).findByText(/Use only lowercase letters/)).toBeInTheDocument()
  })

  it('removing a project CLI keeps the shared accounts and the grants of running tasks', async () => {
    const { user } = await renderConfig({ agent: makeAgent({ clis: [{ name: 'gh', hasTemplate: true, credential: credentialMeta({ envNames: ['GH_TOKEN'] }) }] }) })
    const item = within(region('Available CLIs')).getByRole('listitem', { name: 'gh' })
    await user.click(within(item).getByRole('button', { name: 'Remove' }))
    expect(await screen.findByRole('alertdialog')).toHaveTextContent("the project's shared accounts are kept. Running tasks keep the grants and credentials they started with.")
  })
})

describe('The "Irreversible" stamp when removing a CLI', () => {
  // Removal can't be undone only when the stored credential is deleted along with it; a removal without a credential,
  // or at project level, can be added back, so it gets no stamp (it still looks like a dangerous action).
  const removeDialog = async (agent: Parameters<typeof makeAgent>[0]) => {
    const { user } = await renderConfig({ agent: makeAgent(agent) })
    await user.click(within(within(region('Available CLIs')).getByRole('listitem', { name: 'gh' })).getByRole('button', { name: 'Remove' }))
    return screen.findByRole('alertdialog')
  }
  it('a platform Agent removing a CLI with a credential: the credential is deleted too, so the stamp shows', async () => {
    const dialog = await removeDialog({ scope: 'platform', projectId: null, clis: [{ name: 'gh', hasTemplate: true, credential: credentialMeta({ envNames: ['GH_TOKEN'] }) }] })
    expect(within(dialog).getByText('Irreversible')).toBeInTheDocument()
  })
  it('a CLI without a credential, and a project CLI: no stamp', async () => {
    let dialog = await removeDialog({ scope: 'platform', projectId: null, clis: [{ name: 'gh', hasTemplate: true, credential: null }] })
    expect(dialog.querySelector('[data-dialog-tab]')).toHaveTextContent('Danger')
    expect(within(dialog).queryByText('Irreversible')).not.toBeInTheDocument()
    cleanup()
    dialog = await removeDialog({ clis: [{ name: 'gh', hasTemplate: true, credential: credentialMeta({ envNames: ['GH_TOKEN'] }) }] })
    expect(within(dialog).queryByText('Irreversible')).not.toBeInTheDocument()
  })
})

describe('Credentials are write-only', () => {
  it('a saved gh credential shows only its status and metadata', async () => {
    await renderConfig({ agent: makeAgent({ scope: 'platform', projectId: null, clis: [{ name: 'gh', hasTemplate: true, credential: credentialMeta({ envNames: ['GH_TOKEN'] }) }] }) })
    const item = within(region('Available CLIs')).getByRole('listitem', { name: 'gh' })
    expect(within(item).getByText('Set')).toBeInTheDocument()
    expect(within(item).getByText('Injected environment variables: GH_TOKEN')).toBeInTheDocument()
    expect(within(item).getByText(/Updated/)).toBeInTheDocument()
    expect(await within(item).findByText('Built-in template v2.100.0')).toBeInTheDocument()
  })

  it('entering a lark-cli credential: masked input, brand defaults to feishu, cleared after saving, and the plain text stays neither on the page nor in storage', async () => {
    let body: unknown = null
    const agent = makeAgent({ scope: 'platform', projectId: null, clis: [{ name: 'lark-cli', hasTemplate: true, credential: null }] })
    let current = agent
    const { user, router } = await renderConfig({
      agent,
      handlers: [
        http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
        http.put(apiUrl(`/agents/${agent.id}/clis/lark-cli/credential`), async ({ request }) => {
          body = await request.json()
          current = { ...agent, clis: [{ name: 'lark-cli', hasTemplate: true, credential: credentialMeta({ filePaths: ['.lark-cli/config.json'], warnings: larkTemplate.warnings }) }] }
          return HttpResponse.json(current.clis[0].credential)
        }),
      ],
    })
    const item = within(region('Available CLIs')).getByRole('listitem', { name: 'lark-cli' })
    expect((await within(item).findAllByRole('note'))[0]).toHaveTextContent("the host user's own Lark identity")
    await user.click(within(item).getByRole('button', { name: 'Enter credential' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByLabelText('App Secret')).toHaveAttribute('type', 'password')
    expect(within(dialog).getByLabelText('Brand')).toHaveValue('feishu')
    await user.type(within(dialog).getByLabelText('App ID'), 'cli_app_id')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    expect(await within(dialog).findByText('Required')).toBeInTheDocument()
    await user.type(within(dialog).getByLabelText('App Secret'), 'super-secret-value')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(body).toEqual({ fields: { appId: 'cli_app_id', appSecret: 'super-secret-value', brand: 'feishu' } }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(await within(item).findByText('Set')).toBeInTheDocument()
    expect(document.body.innerHTML).not.toContain('super-secret-value')
    expect(router.state.location.pathname + router.state.location.search).not.toContain('super-secret-value')
    expect(JSON.stringify({ ...localStorage })).not.toContain('super-secret-value')
    expect(JSON.stringify({ ...sessionStorage })).not.toContain('super-secret-value')
  })

  it('an unavailable credential asks to be entered again', async () => {
    await renderConfig({ agent: makeAgent({ scope: 'platform', projectId: null, clis: [{ name: 'gh', hasTemplate: true, credential: credentialMeta({ status: 'unavailable' }) }] }) })
    expect(within(region('Available CLIs')).getByText('Unavailable; enter it again')).toBeInTheDocument()
  })


})

describe('Delete Agent', () => {
  it('when idle, deletes after confirming and goes back to the list', async () => {
    let deleted = false
    const agent = makeAgent()
    const { user, router } = await renderConfig({
      agent,
      handlers: [
        // After the deletion the list no longer contains it: the project has no Agents left, so
        // the Agent page stays on the first-use empty state instead of jumping to another Agent.
        http.get(agentsUrl, () => HttpResponse.json({ agents: deleted ? [] : [toListItem(agent)] })),
        http.delete(apiUrl(`/agents/${agent.id}`), () => {
          deleted = true
          return new HttpResponse(null, { status: 204 })
        }),
      ],
    })
    const danger = region('Delete Agent')
    await waitFor(() => expect(within(danger).getByRole('button', { name: 'Delete this Agent' })).toBeEnabled())
    await user.click(within(danger).getByRole('button', { name: 'Delete this Agent' }))
    const confirm = await screen.findByRole('alertdialog')
    expect(confirm).toHaveTextContent('conversation history is kept for reference')
    expect(confirm).toHaveTextContent("The project's shared accounts are kept")
    await user.click(within(confirm).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(deleted).toBe(true))
    await waitFor(() => expect(router.state.location.pathname).toBe(defaultAgentsPath))
  })

  it('with queued messages, deleting is unavailable and the reason is shown', async () => {
    await renderConfig({ status: { busy: true, queued: { interrupt: 0, normal: 1 } } })
    const danger = region('Delete Agent')
    await waitFor(() => expect(within(danger).getByText(/An Agent can only be deleted while idle/)).toBeInTheDocument())
    expect(within(danger).getByRole('button', { name: 'Delete this Agent' })).toBeDisabled()
  })

  it('turning busy just as it is deleted shows the reason', async () => {
    const agent = makeAgent()
    const { user } = await renderConfig({
      agent,
      handlers: [http.delete(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(errorBody('agent_not_idle', 'busy'), { status: 409 }))],
    })
    const danger = region('Delete Agent')
    await waitFor(() => expect(within(danger).getByRole('button', { name: 'Delete this Agent' })).toBeEnabled())
    await user.click(within(danger).getByRole('button', { name: 'Delete this Agent' }))
    const confirm = await screen.findByRole('alertdialog')
    await user.click(within(confirm).getByRole('button', { name: 'Delete' }))
    expect(await within(confirm).findByText(/The Agent isn't idle/)).toBeInTheDocument()
  })
})
