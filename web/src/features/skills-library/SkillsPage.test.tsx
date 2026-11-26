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

describe('Template list and search rail', () => {
  it('lists templates in the master rail with name, file count and description, and filters by search', async () => {
    const { user } = setup('/skills', [], [summary('a', 'pdf-tools', 'Work with PDF files'), summary('b', 'git-helper', 'Help with git operations')])
    const list = await screen.findByRole('list', { name: 'Skills library' })
    await waitFor(() => expect(within(list).getAllByRole('link')).toHaveLength(2))
    expect(within(list).getByText('Help with git operations')).toBeInTheDocument()
    expect(within(list).getAllByText('2 files')).toHaveLength(2)

    await user.type(screen.getByRole('searchbox', { name: 'Filter skills' }), 'git')
    expect(within(list).getAllByRole('link')).toHaveLength(1)
    expect(within(list).getByText('git-helper')).toBeInTheDocument()
    expect(screen.getByText('1/2')).toBeInTheDocument()
  })

  it('shows the empty state and keeps header actions mounted when the library is empty', async () => {
    setup('/skills')
    const emptyTitle = await screen.findByText('No templates in the Skills library yet.')
    const empty = emptyTitle.closest('[data-empty-library]') as HTMLElement
    expect(within(empty).getByRole('button', { name: 'Upload folder' })).toBeInTheDocument()
    expect(within(empty).getByRole('button', { name: 'Upload zip' })).toBeInTheDocument()
    expect(within(empty).getByRole('link', { name: 'Create online' })).toBeInTheDocument()

    const header = screen.getByRole('heading', { level: 1, name: 'Skills library' }).closest('header')!
    expect(within(header).getByRole('button', { name: 'Upload folder' })).toBeInTheDocument()
    expect(within(header).getByRole('button', { name: 'Upload zip' })).toBeInTheDocument()
    expect(within(header).getByRole('link', { name: 'Create online' })).toBeInTheDocument()
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
  it('renders inline with a prefilled skeleton; saving creates the template', async () => {
    let body: unknown = null
    const { user, router } = setup('/skills/new', [
      http.post(apiUrl('/skills'), async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(detail(), { status: 201 })
      }),
    ])
    const pane = await screen.findByRole('region', { name: 'New Skill' })
    expect(screen.queryByRole('dialog')).toBeNull()
    const editor = within(pane).getByLabelText('SKILL.md')
    expect((editor as HTMLTextAreaElement).value).toMatch(/^---\nname: .+\ndescription: .+\n---/)
    await user.click(within(pane).getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(body).toEqual({ skillMd: (editor as HTMLTextAreaElement).value }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/skills/sk-1'))
  })

  it('without a description shows the reason and keeps the content', async () => {
    const { user } = setup('/skills/new', [
      http.post(apiUrl('/skills'), () => HttpResponse.json(errorBody('validation_failed', 'The frontmatter is missing description'), { status: 422 })),
    ])
    const pane = await screen.findByRole('region', { name: 'New Skill' })
    const editor = within(pane).getByLabelText('SKILL.md')
    await user.clear(editor)
    await user.type(editor, '---{Enter}name: x{Enter}---')
    await user.click(within(pane).getByRole('button', { name: 'Create' }))
    expect(await within(pane).findByText(/missing description/)).toBeInTheDocument()
    expect(editor).toHaveValue('---\nname: x\n---')
  })
})

describe("View and edit a template's SKILL.md and files", () => {
  it('disables Save until edited, submits only SKILL.md on Save or Cmd+S, and shows the Files tab', async () => {
    let body: unknown = null
    const { user } = setup('/skills/sk-1', [
      http.put(apiUrl('/skills/sk-1/skill-md'), async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(detail({ updatedAt: '2026-10-12T01:00:00Z' }))
      }),
    ])
    const pane = await screen.findByRole('region', { name: 'pdf-tools' })
    expect(screen.queryByRole('dialog')).toBeNull()
    const saveButton = within(pane).getByRole('button', { name: /Save/ })
    expect(saveButton).toBeDisabled()

    const editor = within(pane).getByLabelText('SKILL.md')
    await user.type(editor, 'Body')
    expect(saveButton).toBeEnabled()
    expect(pane.querySelector('[data-dirty-dot]')).not.toBeNull()
    await user.click(saveButton)
    await waitFor(() => expect(body).toEqual({ content: '---\nname: pdf-tools\ndescription: Work with PDF files\n---\nBody' }))

    await user.click(within(pane).getByRole('tab', { name: /Files/ }))
    const filesTable = await within(pane).findByRole('table', { name: 'Files' })
    expect(within(filesTable).getByText('scripts/run.sh')).toBeInTheDocument()
  })

  it('when a background refresh fails keeps the unsaved edits and shows the reason', async () => {
    let fail = false
    const { user, client } = setup('/skills/sk-1', [
      http.get(apiUrl('/skills/sk-1'), () => (fail ? HttpResponse.json(errorBody('internal_error'), { status: 500 }) : HttpResponse.json(detail()))),
    ])
    const pane = await screen.findByRole('region', { name: 'pdf-tools' })
    const editor = within(pane).getByLabelText('SKILL.md')
    await user.type(editor, 'Not saved yet')
    fail = true
    await act(() => client.refetchQueries({ queryKey: keys.skill('sk-1') }))
    expect(await within(pane).findByText('Internal server error')).toBeInTheDocument()
    expect(within(pane).getByLabelText('SKILL.md')).toHaveValue('---\nname: pdf-tools\ndescription: Work with PDF files\n---\nNot saved yet')
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
    const pane = await screen.findByRole('region', { name: 'pdf-tools' })
    // The page header also has "Upload folder" for creating; replacing uses the one inside the detail pane.
    await user.upload(within(pane).getByLabelText('Upload folder'), [withPath('---\nname: pdf-tools\n---', 'pdf-tools/SKILL.md')])
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
    const pane = await screen.findByRole('region', { name: 'pdf-tools' })
    await user.click(within(pane).getByRole('button', { name: 'Delete' }))
    const confirm = await screen.findByRole('alertdialog')
    expect(confirm).toHaveTextContent('Skill copies already given to Agents are not affected')
    await user.click(within(confirm).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(deleted).toBe(true))
    await waitFor(() => expect(router.state.location.pathname).toBe('/skills'))
  })
})

describe('Master-detail URL navigation', () => {
  const templates = [summary('sk-1', 'pdf-tools', 'Work with PDF files'), summary('b', 'git-helper', 'Help with git operations')]

  it('opening /skills/:skillId shows the rail and inline detail pane together without a modal backdrop', async () => {
    setup('/skills/sk-1', [], templates)
    const pane = await screen.findByRole('region', { name: 'pdf-tools' })
    expect(within(pane).getByText('Work with PDF files')).toBeInTheDocument()
    const list = await screen.findByRole('list', { name: 'Skills library' })
    await waitFor(() => expect(within(list).getAllByRole('link')).toHaveLength(2))
    expect(within(list).getByRole('link', { name: /pdf-tools/ })).toHaveAttribute('aria-current', 'page')
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('the Cancel button of the inline create pane returns to /skills without creating a template', async () => {
    const { user, router } = setup('/skills/new', [], templates)
    const pane = await screen.findByRole('region', { name: 'New Skill' })
    await user.click(within(pane).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/skills'))
    expect(screen.queryByRole('region', { name: 'New Skill' })).toBeNull()
  })

  it('clicking a template row opens its inline detail pane in one click', async () => {
    const { user, router } = setup('/skills', [], templates)
    const list = await screen.findByRole('list', { name: 'Skills library' })
    await user.click(await within(list).findByRole('link', { name: /pdf-tools/ }))
    expect(await screen.findByRole('region', { name: 'pdf-tools' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/skills/sk-1')
  })

  it("when the template doesn't exist the inline detail pane says why", async () => {
    setup('/skills/gone', [http.get(apiUrl('/skills/gone'), () => HttpResponse.json(errorBody('not_found'), { status: 404 }))], templates)
    const pane = await screen.findByRole('region', { name: 'Template details' })
    expect(await within(pane).findByText("This template doesn't exist or was deleted.")).toBeInTheDocument()
  })
})

describe('Skills library on the sheet', () => {
  it('the header has breadcrumb "Global›Skills library", template count, and right-aligned xs actions', async () => {
    setup('/skills')
    const header = (await screen.findByRole('heading', { level: 1, name: 'Skills library' })).closest('header')!
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('Global›Skills library')
    expect(await within(header).findByText('0 templates')).toBeInTheDocument()
    expect(within(header).getByRole('link', { name: 'Create online' })).toHaveClass('bg-surface-hover', '[[data-skin=plate]_&]:bg-riso-pink')
  })

  it('the empty state preserves mono and Plate styling on the sheet', async () => {
    setup('/skills')
    const title = await screen.findByText('No templates in the Skills library yet.')
    expect(title).toHaveClass('riso-marker', 'font-display-cn')
    const empty = title.closest('[data-empty-library]') as HTMLElement
    expect(empty.closest('[data-sheet]')).not.toBeNull()
    expect(empty.querySelector('[data-empty-art]')).toHaveAttribute('aria-hidden', 'true')
    for (const name of ['Upload folder', 'Upload zip']) {
      const button = within(empty).getByRole('button', { name })
      expect(button).toHaveClass('border', 'border-hairline-strong', 'bg-surface-control', '[[data-skin=plate]_&]:border-2', '[[data-skin=plate]_&]:border-ink', '[[data-skin=plate]_&]:bg-white')
      expect(button).not.toHaveClass('[[data-skin=plate]_&]:bg-riso-pink')
    }
    expect(within(empty).getByRole('link', { name: 'Create online' })).toHaveClass('bg-surface-hover', '[[data-skin=plate]_&]:bg-riso-pink', '[[data-skin=plate]_&]:border-2', '[[data-skin=plate]_&]:border-ink')
  })

  it('selected rail row uses surface-active in mono skin and yellow with ink outline and shadow in Plate skin', async () => {
    setup('/skills/sk-1', [], [summary('sk-1', 'pdf-tools', 'Work with PDF files'), summary('sk-2', 'csv-tools', 'Work with CSV files')])
    const list = await screen.findByRole('list', { name: 'Skills library' })
    const [open, other] = within(list).getAllByRole('link')
    expect(open).toHaveAttribute('aria-current', 'page')
    expect(open).toHaveClass(
      'bg-surface-active',
      '[[data-skin=plate]_&]:border-ink',
      '[[data-skin=plate]_&]:bg-riso-yellow',
      '[[data-skin=plate]_&]:shadow-[3px_3px_0_var(--ink)]',
    )
    expect(other.closest('[data-sheet]')).not.toBeNull()
  })
})
