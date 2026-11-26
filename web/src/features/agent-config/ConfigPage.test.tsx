import { act, screen, waitFor, within, cleanup } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { routes } from '@/app/routes'
import { agentsUrl, apiUrl, defaultAgentPath, defaultAgentsPath, errorBody, makeAgent, makeRun, toListItem } from '@/test/api'
import { catalogHandlers, credentialMeta, ghTemplate, larkTemplate, renderConfig } from '@/test/config'
import { FakeIntersectionObserver } from '@/test/intersection-observer'
import { openProvider, runnerTab } from '@/test/model-picker'
import { emptySelections } from '@/test/project-credentials'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'
import userEvent from '@testing-library/user-event'

const region = (name: string) => screen.getByRole('region', { name })
const tabPanel = (key: string) => document.querySelector<HTMLElement>(`[data-tab-panel="${key}"]`)!
const openRunner = async (user: Awaited<ReturnType<typeof renderConfig>>['user']) => {
  const trigger = within(region('Basics')).getByRole('button', { name: /^Runs on/ })
  if (trigger.getAttribute('aria-expanded') !== 'true') await user.click(trigger)
  return screen.findByRole('dialog', { name: 'Runs on' })
}
const expandCli = async (user: Awaited<ReturnType<typeof renderConfig>>['user'], cli: string) => {
  const item = within(region('Available CLIs')).getByRole('listitem', { name: cli })
  const toggle = within(item).getByRole('button', { name: new RegExp(`^${cli}\\b`) })
  if (toggle.getAttribute('aria-expanded') !== 'true') await user.click(toggle)
  return item
}

function renderConfigAt(path: string, agent = makeAgent()) {
  server.use(
    ...baseHandlers([agent]),
    http.get(apiUrl(`/agents/${agent.id}/credential-selections`), () => HttpResponse.json(emptySelections(agent))),
    http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => HttpResponse.json({ credential: agent.modelCredential, operation: null })),
    http.get(apiUrl(`/agents/${agent.id}/skills`), () => HttpResponse.json({ skills: [] })),
    http.get(apiUrl('/skills'), () => HttpResponse.json({ skills: [] })),
    http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [ghTemplate, larkTemplate] })),
    ...catalogHandlers,
  )
  return { ...renderRoutes(routes, path), agent, user: userEvent.setup() }
}

describe('Layout', () => {
  it('"The current reply uses the config from before the change" shows in the compact header status pill alongside the effect notice', async () => {
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
    expect(region('Basics').closest('[data-page-body]')).toHaveClass('relative', 'overflow-y-auto')
  })
})

describe('Sub-tab navigation and deep links', () => {
  it('organizes config into 4 crisp sub-tabs synced with ?tab= and removes the 196px right-hand ConfigToc stub', async () => {
    const { user, router } = await renderConfig()
    const tablist = screen.getByRole('tablist', { name: 'Configuration tabs' })
    const tabs = within(tablist).getAllByRole('tab')
    expect(tabs.map((tab) => tab.textContent)).toEqual(['General & Model', 'Prompt & Workspace', 'Tools & Access', 'Danger'])
    expect(tabs[0]).toHaveAttribute('aria-selected', 'true')
    expect(tabs[3].querySelector('.text-red-text')).not.toBeNull()

    // Active tab panel is visible; inactive panels are hidden via CSS while staying mounted for draft safety.
    expect(tabPanel('general')).not.toHaveClass('hidden')
    expect(tabPanel('workspace')).toHaveClass('hidden')
    expect(tabPanel('tools')).toHaveClass('hidden')
    expect(tabPanel('danger')).toHaveClass('hidden')

    // Centered narrow PageBody + floating PrecisionRuler (zero-width-tax), no 196px right-hand stub.
    expect(screen.getByRole('navigation', { name: 'Contents' })).toHaveAttribute('data-precision-ruler')
    expect(document.querySelector('.w-\\[196px\\]')).toBeNull()

    await user.click(within(tablist).getByRole('tab', { name: 'Prompt & Workspace' }))
    expect(router.state.location.search).toBe('?tab=workspace')
    expect(tabPanel('general')).toHaveClass('hidden')
    expect(tabPanel('workspace')).not.toHaveClass('hidden')

    await user.click(within(tablist).getByRole('tab', { name: 'Tools & Access' }))
    expect(router.state.location.search).toBe('?tab=tools')
    expect(tabPanel('tools')).not.toHaveClass('hidden')

    await user.click(within(tablist).getByRole('tab', { name: 'Danger' }))
    expect(router.state.location.search).toBe('?tab=danger')
    expect(tabPanel('danger')).not.toHaveClass('hidden')
    // Danger tab has only 1 section, so the floating ruler hides itself.
    expect(screen.queryByRole('navigation', { name: 'Contents' })).toBeNull()
  })

  it('preserves unsaved drafts across sub-tab switches without blocking tab switches, and still guards route exits', async () => {
    const { user, router } = await renderConfig()
    const tablist = screen.getByRole('tablist', { name: 'Configuration tabs' })
    await user.type(screen.getByLabelText('Display name'), ' Draft')
    await user.click(within(tablist).getByRole('tab', { name: 'Prompt & Workspace' }))
    expect(screen.queryByRole('alertdialog')).toBeNull()
    await user.type(screen.getByLabelText('Agent prompt'), 'Unsaved instructions')

    await user.click(within(tablist).getByRole('tab', { name: 'General & Model' }))
    expect(screen.getByLabelText('Display name')).toHaveValue('dev-helper Draft')

    await user.click(within(tablist).getByRole('tab', { name: 'Prompt & Workspace' }))
    expect(screen.getByLabelText('Agent prompt')).toHaveValue('Unsaved instructions')

    // Leaving the config page still asks for confirmation.
    await user.click(screen.getByRole('link', { name: 'Chat' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('You have unsaved changes')
    expect(router.state.location.pathname).toMatch(/\/config$/)
  })

  it('resolves legacy #config-* hashes (such as #config-prompt from StatsPage and #config-model-account) to their owning tab and focuses the section title', async () => {
    const scroll = vi.spyOn(Element.prototype, 'scrollIntoView')
    try {
      const agent = makeAgent()
      const first = renderConfigAt(`${defaultAgentPath(agent.id, 'config')}#config-prompt`, agent)
      await screen.findByRole('tablist', { name: 'Configuration tabs' })
      expect(screen.getByRole('tab', { name: 'Prompt & Workspace' })).toHaveAttribute('aria-selected', 'true')
      expect(tabPanel('workspace')).not.toHaveClass('hidden')
      expect(tabPanel('general')).toHaveClass('hidden')
      expect(within(region('Prompt')).getByRole('heading', { level: 2, name: 'Prompt' })).toHaveFocus()
      expect(scroll.mock.contexts).toContain(region('Prompt'))
      first.unmount()

      const second = renderConfigAt(`${defaultAgentPath(agent.id, 'config')}#config-clis`, agent)
      await screen.findByRole('tablist', { name: 'Configuration tabs' })
      expect(screen.getByRole('tab', { name: 'Tools & Access' })).toHaveAttribute('aria-selected', 'true')
      expect(tabPanel('tools')).not.toHaveClass('hidden')
      expect(within(region('Available CLIs')).getByRole('heading', { level: 2, name: 'Available CLIs' })).toHaveFocus()
      second.unmount()

      const third = renderConfigAt(`${defaultAgentPath(agent.id, 'config')}#config-model-account`, agent)
      await screen.findByRole('tablist', { name: 'Configuration tabs' })
      expect(screen.getByRole('tab', { name: 'General & Model' })).toHaveAttribute('aria-selected', 'true')
      expect(tabPanel('general')).not.toHaveClass('hidden')
      expect(within(region('Model account credential')).getByRole('heading', { level: 2, name: 'Model account credential' })).toHaveFocus()
      third.unmount()
    } finally {
      scroll.mockRestore()
    }
  })

  it('sections use compact headings with hairline dividers and red danger styling', async () => {
    await renderConfig()
    const basic = region('Basics')
    expect(basic).not.toHaveClass('shadow-print-rest', 'bg-paper-raised')
    expect(within(basic).getByRole('heading', { level: 2, name: 'Basics' })).toHaveClass('text-[14.5px]', 'font-semibold')
    expect(basic.querySelector('[data-section-number]')).toBeNull()
    expect(within(basic).getByLabelText('Display name').closest('.grid')!.parentElement).toHaveClass('sm:items-start')

    const danger = region('Delete Agent')
    expect(within(danger).getByRole('heading', { level: 2, name: 'Delete Agent' })).toHaveClass('text-red-text')
    expect(danger.querySelector('[data-section-body]')).toHaveClass('riso-stripes-red', 'bg-error-field')
  })

  it('the internal ID sits in a subtle hairline box in mono skin and a dashed box with a yellow navy-outlined copy button in Plate skin', async () => {
    const { agent } = await renderConfig()
    const id = within(region('Basics')).getByText(agent.id)
    expect(id.parentElement).toHaveClass('rounded-input', 'border', 'border-hairline', '[[data-skin=plate]_&]:border-dashed')
    expect(within(region('Basics')).getByRole('button', { name: 'Copy' })).toHaveClass(
      '[[data-skin=plate]_&]:border-2',
      '[[data-skin=plate]_&]:border-ink',
      '[[data-skin=plate]_&]:bg-riso-yellow',
    )
  })

  it('floating PageRuler tracks the visible section in the active tab and jumps on click', async () => {
    const scroll = vi.spyOn(Element.prototype, 'scrollIntoView')
    try {
      const { user } = await renderConfig()
      const ruler = screen.getByRole('navigation', { name: 'Contents' })
      expect(within(ruler).getAllByRole('button').map((btn) => btn.getAttribute('aria-label'))).toEqual([
        'Basics',
        'Model account credential',
        'Permissions',
      ])
      act(() => FakeIntersectionObserver.trigger(region('Permissions'), true))
      expect(within(ruler).getByRole('button', { name: 'Permissions' })).toHaveAttribute('aria-current', 'true')
      await user.click(within(ruler).getByRole('button', { name: 'Model account credential' }))
      expect(within(region('Model account credential')).getByRole('heading', { level: 2, name: 'Model account credential' })).toHaveFocus()
      expect(scroll.mock.contexts).toContain(region('Model account credential'))
    } finally {
      scroll.mockRestore()
    }
  })
})

describe('Name, model and Prompt', () => {
  it("shows the read-only internal ID and compact model trigger; opening the picker lists runtimes first and each provider's models", async () => {
    const { agent, user } = await renderConfig()
    const basic = region('Basics')
    expect(within(basic).getByText(agent.id)).toBeInTheDocument()
    await waitFor(() => expect(within(basic).getByRole('button', { name: /^Runs on/ })).toHaveTextContent(/anthropic.*Claude Sonnet 5\.5/))
    const picker = await openRunner(user)
    expect(await runnerTab(picker, 'Pi')).toHaveAttribute('aria-selected', 'true')
    await waitFor(() => expect(within(picker).getByRole('treeitem', { name: 'Claude Sonnet 5.5 (claude-sonnet-5-5)' })).toHaveAttribute('aria-selected', 'true'))
    await openProvider(user, picker, 'openai')
    const openai = within(picker).getAllByRole('treeitem').find((row) => row.getAttribute('aria-label') === 'openai')!
    expect(within(openai).getAllByRole('treeitem').map((row) => row.getAttribute('aria-label'))).toEqual(['GPT X (gpt-x)'])
  })

  it('marks the current model when it is not in the catalog', async () => {
    await renderConfig({ agent: makeAgent({ model: { provider: 'anthropic', modelId: 'retired-model' } }) })
    const basic = region('Basics')
    expect(await within(basic).findByText("anthropic / retired-model, used now, isn't in the model catalog")).toHaveAttribute('role', 'note')
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
  it('adding a CLI without a template through the header dialog: marker only, no credential entry', async () => {
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
    await user.click(within(clis).getByRole('button', { name: 'Add CLI' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add CLI' })
    await user.type(within(dialog).getByLabelText('CLI name'), 'jq')
    await user.click(within(dialog).getByRole('button', { name: 'Add CLI' }))
    await waitFor(() => expect(added).toBe('jq'))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    const item = await within(clis).findByRole('listitem', { name: 'jq' })
    expect(item).toHaveTextContent('Marker only: not installed, no credential injected')
    expect(within(item).queryByRole('button', { name: /credential/i })).toBeNull()
  })

  it('a network error keeps the input in the dialog and says the server cannot be reached', async () => {
    const agent = makeAgent()
    const { user } = await renderConfig({ agent, handlers: [http.put(apiUrl(`/agents/${agent.id}/clis/:cli`), () => HttpResponse.error())] })
    await user.click(within(region('Available CLIs')).getByRole('button', { name: 'Add CLI' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add CLI' })
    await user.type(within(dialog).getByLabelText('CLI name'), 'gh')
    await user.click(within(dialog).getByRole('button', { name: 'Add CLI' }))
    expect(await within(dialog).findByText('Can\'t reach the server')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('CLI name')).toHaveValue('gh')
  })

  it('an invalid name is not submitted', async () => {
    const { user } = await renderConfig()
    await user.click(within(region('Available CLIs')).getByRole('button', { name: 'Add CLI' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add CLI' })
    await user.type(within(dialog).getByLabelText('CLI name'), 'Bad Name')
    await user.click(within(dialog).getByRole('button', { name: 'Add CLI' }))
    expect(await within(dialog).findByText(/Use only lowercase letters/)).toBeInTheDocument()
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
  it('a saved gh credential shows its status on the summary row and full metadata when expanded', async () => {
    const { user } = await renderConfig({ agent: makeAgent({ scope: 'platform', projectId: null, clis: [{ name: 'gh', hasTemplate: true, credential: credentialMeta({ envNames: ['GH_TOKEN'] }) }] }) })
    const item = await expandCli(user, 'gh')
    expect(within(item).getAllByText('Set').length).toBeGreaterThan(0)
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
    const item = await expandCli(user, 'lark-cli')
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
    expect((await within(item).findAllByText('Set')).length).toBeGreaterThan(0)
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
