import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse, type HttpHandler } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { keys } from '@/api/queries'
import { apiUrl, context, errorBody } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'

const summary = (id: string, name: string, description: string): Schemas['SkillTemplateSummary'] => ({
  id,
  name,
  description,
  fileCount: 2,
  createdAt: '2026-10-12T00:00:00Z',
  updatedAt: '2026-10-12T00:00:00Z',
})

const detail = (overrides: Partial<Schemas['SkillTemplate']> = {}): Schemas['SkillTemplate'] => ({
  id: 'sk-1',
  name: 'pdf-tools',
  description: 'Work with PDF files',
  files: [
    { path: 'SKILL.md', size: 40, sha256: 'a' },
    { path: 'scripts/run.sh', size: 7, sha256: 'b' },
  ],
  skillMd: '---\nname: pdf-tools\ndescription: Work with PDF files\n---\n',
  createdAt: '2026-10-12T00:00:00Z',
  updatedAt: '2026-10-12T00:00:00Z',
  ...overrides,
})

function setup(path: string, handlers: HttpHandler[] = [], skills: Schemas['SkillTemplateSummary'][] = []) {
  server.use(
    ...handlers,
    http.get(apiUrl('/context'), () => HttpResponse.json(context)),
    http.get(apiUrl('/skills'), () => HttpResponse.json({ skills })),
    http.get(apiUrl('/skills/sk-1'), () => HttpResponse.json(detail())),
  )
  const user = userEvent.setup()
  return { user, ...renderRoutes(routes, path) }
}

function withPath(content: string, relativePath: string) {
  const file = new File([content], relativePath.split('/').at(-1)!)
  Object.defineProperty(file, 'webkitRelativePath', { value: relativePath })
  return file
}

describe('Template list', () => {
  it('lists the templates with their name, description and update time', async () => {
    setup('/skills', [], [summary('a', 'pdf-tools', 'Work with PDF files'), summary('b', 'git-helper', 'Help with git operations')])
    const list = await screen.findByRole('list', { name: 'Skills library' })
    await waitFor(() => expect(within(list).getAllByRole('link')).toHaveLength(2))
    expect(within(list).getByText('Help with git operations')).toBeInTheDocument()
    expect(within(list).getAllByText(/Updated/)).toHaveLength(2)
  })

  it('shows the empty state with the upload and create entries when the library is empty', async () => {
    setup('/skills')
    expect(await screen.findByText('No templates in the Skills library yet.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Upload folder' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Upload zip' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Create online' })).toBeInTheDocument()
  })
})

describe('Upload Skill', () => {
  it('upload folder: packs it as a zip, uploads it as multipart and opens the template on success', async () => {
    let contentType: string | null = null
    let hasArchive = false
    const { user, router } = setup('/skills', [
      http.post(apiUrl('/skills'), async ({ request }) => {
        contentType = request.headers.get('Content-Type')
        // Multipart file content is unreliable under jsdom (see test/setup.ts), so only the field name is checked here.
        hasArchive = (await request.text()).includes('name="archive"')
        return HttpResponse.json(detail(), { status: 201 })
      }),
    ])
    await user.upload(await screen.findByLabelText('Upload folder'), [withPath('---\nname: pdf-tools\n---', 'pdf-tools/SKILL.md'), withPath('echo hi', 'pdf-tools/scripts/run.sh')])
    await waitFor(() => expect(router.state.location.pathname).toBe('/skills/sk-1'))
    expect(contentType).toMatch(/^multipart\/form-data/)
    expect(hasArchive).toBe(true)
  })

  it('without SKILL.md shows the reason and adds no template', async () => {
    const { user, router } = setup('/skills', [
      http.post(apiUrl('/skills'), () => HttpResponse.json(errorBody('validation_failed', 'SKILL.md is missing'), { status: 422 })),
    ])
    await user.upload(await screen.findByLabelText('Upload folder'), [withPath('echo hi', 'x/run.sh')])
    expect(await screen.findByText(/SKILL.md is missing/)).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/skills')
  })

  it('on a duplicate name suggests using Replace instead', async () => {
    const { user } = setup('/skills', [http.post(apiUrl('/skills'), () => HttpResponse.json(errorBody('name_conflict', 'dup'), { status: 409 }))])
    await user.upload(await screen.findByLabelText('Upload zip'), new File(['zip'], 'pdf-tools.zip', { type: 'application/zip' }))
    expect(await screen.findByText(/open that template and use Replace/)).toBeInTheDocument()
  })
})

describe('Create Skill online', () => {
  it('the editor is prefilled with a skeleton; saving creates the template', async () => {
    let body: unknown = null
    const { user, router } = setup('/skills/new', [
      http.post(apiUrl('/skills'), async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(detail(), { status: 201 })
      }),
    ])
    const sheet = await screen.findByRole('dialog', { name: 'New Skill' })
    const editor = within(sheet).getByLabelText('SKILL.md')
    expect((editor as HTMLTextAreaElement).value).toMatch(/^---\nname: .+\ndescription: .+\n---/)
    await user.click(within(sheet).getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(body).toEqual({ skillMd: (editor as HTMLTextAreaElement).value }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/skills/sk-1'))
  })

  it('without a description shows the reason and keeps the content', async () => {
    const { user } = setup('/skills/new', [
      http.post(apiUrl('/skills'), () => HttpResponse.json(errorBody('validation_failed', 'The frontmatter is missing description'), { status: 422 })),
    ])
    const sheet = await screen.findByRole('dialog', { name: 'New Skill' })
    const editor = within(sheet).getByLabelText('SKILL.md')
    await user.clear(editor)
    await user.type(editor, '---{Enter}name: x{Enter}---')
    await user.click(within(sheet).getByRole('button', { name: 'Create' }))
    expect(await within(sheet).findByText(/missing description/)).toBeInTheDocument()
    expect(editor).toHaveValue('---\nname: x\n---')
  })
})

describe('View and edit a template\'s SKILL.md', () => {
  it('shows the file list; saving submits only SKILL.md', async () => {
    let body: unknown = null
    const { user } = setup('/skills/sk-1', [
      http.put(apiUrl('/skills/sk-1/skill-md'), async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(detail())
      }),
    ])
    const sheet = await screen.findByRole('dialog', { name: 'pdf-tools' })
    const files = await within(sheet).findByRole('list', { name: 'Files' })
    expect(within(files).getByText('scripts/run.sh')).toBeInTheDocument()
    const editor = within(sheet).getByLabelText('SKILL.md')
    await user.type(editor, 'Body')
    await user.click(within(sheet).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(body).toEqual({ content: '---\nname: pdf-tools\ndescription: Work with PDF files\n---\nBody' }))
  })

  it('when a background refresh fails keeps the unsaved edits and shows the reason', async () => {
    let fail = false
    const { user, client } = setup('/skills/sk-1', [
      http.get(apiUrl('/skills/sk-1'), () => (fail ? HttpResponse.json(errorBody('internal_error'), { status: 500 }) : HttpResponse.json(detail()))),
    ])
    const sheet = await screen.findByRole('dialog', { name: 'pdf-tools' })
    const editor = within(sheet).getByLabelText('SKILL.md')
    await user.type(editor, 'Not saved yet')
    fail = true
    await act(() => client.refetchQueries({ queryKey: keys.skill('sk-1') }))
    expect(await within(sheet).findByText('Internal server error')).toBeInTheDocument()
    expect(within(sheet).getByLabelText('SKILL.md')).toHaveValue('---\nname: pdf-tools\ndescription: Work with PDF files\n---\nNot saved yet')
  })
})

describe('Replace and delete templates', () => {
  it('asks for confirmation before replacing and says that copies are not affected', async () => {
    let replaced = false
    const { user } = setup('/skills/sk-1', [
      http.put(apiUrl('/skills/sk-1'), () => {
        replaced = true
        return HttpResponse.json(detail())
      }),
    ])
    const sheet = await screen.findByRole('dialog', { name: 'pdf-tools' })
    await within(sheet).findByRole('list', { name: 'Files' })
    // The grid's toolbar also has “Upload folder”, used for creating; replacing uses only the one in the drawer.
    await user.upload(within(sheet).getByLabelText('Upload folder'), [withPath('---\nname: pdf-tools\n---', 'pdf-tools/SKILL.md')])
    const confirm = await screen.findByRole('alertdialog')
    expect(confirm).toHaveTextContent('Skill copies already given to Agents are not affected')
    expect(replaced).toBe(false)
    await user.click(within(confirm).getByRole('button', { name: 'Replace' }))
    await waitFor(() => expect(replaced).toBe(true))
  })

  it('asks for confirmation before deleting and returns to the list afterwards', async () => {
    let deleted = false
    const { user, router } = setup('/skills/sk-1', [
      http.delete(apiUrl('/skills/sk-1'), () => {
        deleted = true
        return new HttpResponse(null, { status: 204 })
      }),
    ])
    const sheet = await screen.findByRole('dialog', { name: 'pdf-tools' })
    await within(sheet).findByRole('list', { name: 'Files' })
    await user.click(within(sheet).getByRole('button', { name: 'Delete' }))
    const confirm = await screen.findByRole('alertdialog')
    expect(confirm).toHaveTextContent('Skill copies already given to Agents are not affected')
    await user.click(within(confirm).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(deleted).toBe(true))
    await waitFor(() => expect(router.state.location.pathname).toBe('/skills'))
  })
})

describe('The URL opens and closes the drawer', () => {
  const templates = [summary('sk-1', 'pdf-tools', 'Work with PDF files'), summary('b', 'git-helper', 'Help with git operations')]

  it('opening /skills/:skillId directly shows the grid with the details drawer open; closing it returns to /skills', async () => {
    const { user, router } = setup('/skills/sk-1', [], templates)
    const sheet = await screen.findByRole('dialog', { name: 'pdf-tools' })
    expect(within(sheet).getByText('Work with PDF files')).toBeInTheDocument()
    // The drawer is modal: the grid is temporarily hidden from assistive technology but is still on the page.
    const list = await screen.findByRole('list', { name: 'Skills library', hidden: true })
    await waitFor(() => expect(within(list).getAllByRole('link', { hidden: true })).toHaveLength(2))
    expect(within(list).getByRole('link', { name: /pdf-tools/, hidden: true })).toHaveAttribute('aria-current', 'true')

    await user.click(within(sheet).getByRole('button', { name: 'Close' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/skills'))
    // The drawer opens and closes with the URL, and the new URL reaches the page
    // only inside a transition, so wait until the drawer has actually closed.
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(screen.getByRole('list', { name: 'Skills library' })).toBeInTheDocument()
  })

  it('opening /skills/new directly shows the grid with the create drawer open; Esc closes it and returns to /skills', async () => {
    const { user, router } = setup('/skills/new', [], templates)
    const sheet = await screen.findByRole('dialog', { name: 'New Skill' })
    expect(within(sheet).getByLabelText('SKILL.md')).toBeInTheDocument()
    expect(await screen.findByRole('list', { name: 'Skills library', hidden: true })).toBeInTheDocument()

    await user.keyboard('{Escape}')
    await waitFor(() => expect(router.state.location.pathname).toBe('/skills'))
    // The drawer opens and closes with the URL, and the new URL reaches the page
    // only inside a transition, so wait until the drawer has actually closed.
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('the Cancel button of the create drawer closes it without creating a template', async () => {
    const { user, router } = setup('/skills/new', [], templates)
    const sheet = await screen.findByRole('dialog', { name: 'New Skill' })
    await user.click(within(sheet).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/skills'))
    // The drawer opens and closes with the URL, and the new URL reaches the page
    // only inside a transition, so wait until the drawer has actually closed.
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('clicking a template card opens the details drawer', async () => {
    const { user, router } = setup('/skills', [], templates)
    const list = await screen.findByRole('list', { name: 'Skills library' })
    await user.click(await within(list).findByRole('link', { name: /pdf-tools/ }))
    expect(await screen.findByRole('dialog', { name: 'pdf-tools' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/skills/sk-1')
  })

  it('when the template doesn\'t exist the drawer says why', async () => {
    setup('/skills/gone', [http.get(apiUrl('/skills/gone'), () => HttpResponse.json(errorBody('not_found'), { status: 404 }))], templates)
    const sheet = await screen.findByRole('dialog', { name: 'Template details' })
    expect(await within(sheet).findByText('This template doesn\'t exist or was deleted.')).toBeInTheDocument()
  })
})

// "Printed sheet" redesign, step 4: the Skills library's page header, empty state and template cards.
describe('Skills library on the sheet', () => {
  it('the header is the page title: breadcrumb “Global›Skills library”, next to it the number of templates', async () => {
    setup('/skills')
    const header = (await screen.findByRole('heading', { level: 1, name: 'Skills library' })).closest('header')!
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('Global›Skills library')
    expect(await within(header).findByText('0 templates')).toBeInTheDocument()
  })

  it('the empty state is on the sheet: an overprinted card illustration and a yellow-marker title; secondary upload actions use control surface in mono skin and only “Create online” uses Plate fluorescent pink', async () => {
    setup('/skills')
    const title = await screen.findByText('No templates in the Skills library yet.')
    expect(title).toHaveClass('riso-marker', 'font-display-cn')
    const empty = title.closest('[data-empty-library]')!
    expect(empty.closest('[data-sheet]')).not.toBeNull()
    expect(empty.querySelector('[data-empty-art]')).toHaveAttribute('aria-hidden', 'true')
    for (const name of ['Upload folder', 'Upload zip']) {
      const button = within(empty as HTMLElement).getByRole('button', { name })
      expect(button).toHaveClass('border', 'border-hairline-strong', 'bg-surface-control', '[[data-skin=plate]_&]:border-2', '[[data-skin=plate]_&]:border-ink', '[[data-skin=plate]_&]:bg-white')
      expect(button).not.toHaveClass('[[data-skin=plate]_&]:bg-riso-pink')
    }
    expect(within(empty as HTMLElement).getByRole('link', { name: 'Create online' })).toHaveClass('bg-surface-hover', '[[data-skin=plate]_&]:bg-riso-pink', '[[data-skin=plate]_&]:border-2', '[[data-skin=plate]_&]:border-ink')
  })

  it('with templates: the entries sit in the header meta; templates are crisp control cards in mono skin and preserve white/yellow Plate cards with shadow', async () => {
    setup('/skills/sk-1', [], [summary('sk-1', 'pdf-tools', 'Work with PDF files'), summary('sk-2', 'csv-tools', 'Work with CSV files')])
    const list = await screen.findByRole('list', { name: 'Skills library', hidden: true })
    const header = screen.getByRole('heading', { level: 1, name: 'Skills library', hidden: true }).closest('header')!
    expect(within(header).getByRole('link', { name: 'Create online', hidden: true })).toHaveClass('bg-surface-hover', '[[data-skin=plate]_&]:bg-riso-pink')
    const [open, other] = within(list).getAllByRole('link', { hidden: true })
    expect(open).toHaveAttribute('aria-current', 'true')
    expect(open).toHaveClass(
      'border',
      'border-hairline-strong',
      'bg-surface-control',
      'aria-[current=true]:bg-surface-active',
      '[[data-skin=plate]_&]:aria-[current=true]:bg-riso-yellow',
      '[[data-skin=plate]_&]:border-2',
      '[[data-skin=plate]_&]:border-ink',
      '[[data-skin=plate]_&]:shadow-[4px_4px_0_var(--ink)]',
    )
    expect(other.closest('[data-sheet]')).not.toBeNull()
  })
})
