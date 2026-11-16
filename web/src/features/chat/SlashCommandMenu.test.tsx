import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody, makeMessage, makeRun } from '@/test/api'
import { renderWithQuery } from '@/test/render'
import { claudeCodeList, commandList, piCommand, platformCommands } from '@/test/commands'
import { server } from '@/test/server'
import { Composer } from './Composer'
import { applyCommand, commandText, matchCommands, slashToken } from './slash-commands'

const command = piCommand
const list: Schemas['AgentCommandList'] = {
  runtime: 'pi',
  capturedAt: '2026-11-10T08:00:00Z',
  commands: [
    ...platformCommands(),
    command('review', { description: 'Review the current changes', pluginId: 'reviewer', pluginDisplayName: { 'zh-CN': '代码审查', 'en-US': 'Code review' } }),
    command('skill:loopctl', { piSource: 'skill', description: 'Manage Agents <b>and tasks</b> with loopctl' }),
    command('summarize', { piSource: 'prompt', description: 'Write the conversation up as a summary' }),
  ],
}

// The chat page's input: record how many times the command list is read and which messages are sent.
function setup(commands: () => Response = () => HttpResponse.json(list)) {
  let reads = 0
  const sent: Schemas['SendMessageInput'][] = []
  server.use(
    http.get(apiUrl('/agents/agent-1/commands'), () => { reads += 1; return commands() }),
    http.post(apiUrl('/agents/agent-1/messages'), async ({ request }) => {
      const body = (await request.json()) as Schemas['SendMessageInput']
      sent.push(body)
      const run = makeRun({ status: 'starting', endedAt: null })
      return HttpResponse.json({ run, message: makeMessage({ text: body.text, run }) }, { status: 202 })
    }),
  )
  renderWithQuery(<Composer agentId="agent-1" agentName="开发助手" busy={false} disabled={false} maxMessageBytes={4096} onSent={() => {}} />)
  return { user: userEvent.setup(), input: screen.getByRole('textbox', { name: 'Message' }), sent, reads: () => reads }
}

const options = () => within(screen.getByRole('listbox', { name: 'Commands' })).getAllByRole('option')
const labels = () => options().map((option) => option.querySelector('code')?.textContent)

describe('the "/" command menu of the input box', () => {
  it('reads the command list only once "/" is typed; each row is /pi:<name>, source, plugin and description (the extension text shown as plain text); the input points at the selected item', async () => {
    const { user, input, reads } = setup()
    await user.type(input, 'Hello')
    expect(screen.queryByRole('listbox')).toBeNull()
    expect(reads()).toBe(0)
    await user.clear(input)
    await user.type(input, '/')
    await waitFor(() => expect(labels()).toEqual(['/session', '/new', '/compact [instructions]', '/reload', '/copy', '/export [html|jsonl]', '/resume [session-id]', '/pi:review', '/pi:skill:loopctl', '/pi:summarize']))
    expect(reads()).toBe(1)
    expect(options()[7]).toHaveTextContent('/pi:reviewExtension command · Plugin · Code reviewReview the current changes')
    expect(options()[8]).toHaveTextContent('/pi:skill:loopctlSkillManage Agents <b>and tasks</b> with loopctl')
    expect(options()[9]).toHaveTextContent('/pi:summarizePrompt templateWrite the conversation up as a summary')
    expect(options()[0]).toHaveAttribute('aria-selected', 'true')
    expect(input).toHaveAttribute('aria-activedescendant', options()[0].id)
    expect(input).toHaveAttribute('aria-controls', screen.getByRole('listbox').id)
    expect(input).toHaveFocus()
  })

  // "Printed sheet" redesign: the menu is a small sheet, the selected item is yellow, and the source is written in a small label.
  it('the menu is a small sheet; the selected item is yellow; the source is in a label: platform commands white on navy, Pi commands on light blue', async () => {
    const { user, input } = setup()
    await user.type(input, '/')
    await waitFor(() => expect(options()).toHaveLength(10))
    const panel = screen.getByRole('listbox', { name: 'Commands' }).closest<HTMLElement>('[data-radix-popper-content-wrapper] > *')!
    expect(panel).toHaveClass('bg-paper-raised', 'rounded-card', 'shadow-[0_0_0_2px_var(--ink),4px_4px_0_2px_var(--riso-blue)]')
    for (const option of options()) expect(option).toHaveClass('aria-selected:bg-riso-yellow')
    expect(options()[0]).not.toHaveClass('aria-selected:bg-tint-blue')
    expect(within(options()[0]).getByText('Platform command')).toHaveClass('rounded-full', 'bg-ink', 'text-white')
    expect(within(options()[7]).getByText('Extension command · Plugin · Code review')).toHaveClass('rounded-stamp', 'bg-tint-blue', 'text-ink')
    // A dashed line separates the key hints at the bottom from the list.
    expect(panel.lastElementChild).toHaveClass('border-t-[1.5px]', 'border-dashed')
  })

  it('filters by the first word: those starting with the /pi: form first, then command names starting with it without pi:, then names or descriptions containing it', async () => {
    const { user, input } = setup()
    await user.type(input, '/pi:s')
    await waitFor(() => expect(labels()).toEqual(['/pi:skill:loopctl', '/pi:summarize']))
    await user.clear(input)
    await user.type(input, '/rev')
    await waitFor(() => expect(labels()).toEqual(['/pi:review']))
    await user.clear(input)
    await user.type(input, '/summary')
    await waitFor(() => expect(labels()).toEqual(['/pi:summarize']))
    await user.clear(input)
    await user.type(input, '/nothing')
    expect(await screen.findByRole('status')).toHaveTextContent('No matching commands.')
  })

  it('arrow keys move, Enter fills in "/pi:<name> " without sending; then type arguments and Enter sends the text as usual', async () => {
    const { user, input, sent } = setup()
    await user.type(input, '/pi:')
    await waitFor(() => expect(options()).toHaveLength(3))
    await user.keyboard('{ArrowDown}{ArrowDown}{ArrowDown}{ArrowUp}')
    expect(options()[1]).toHaveAttribute('aria-selected', 'true')
    await user.keyboard('{Enter}')
    expect(input).toHaveValue('/pi:skill:loopctl ')
    expect(screen.queryByRole('listbox')).toBeNull()
    expect(sent).toEqual([])
    await user.type(input, 'list tasks')
    await user.keyboard('{Enter}')
    await waitFor(() => expect(sent).toEqual([{ text: '/pi:skill:loopctl list tasks' }]))
  })

  it('Tab and click fill in the same way; with arguments already typed only the first word is replaced', async () => {
    const { user, input } = setup()
    await user.type(input, '/pi:sum')
    await waitFor(() => expect(options()).toHaveLength(1))
    await user.keyboard('{Tab}')
    expect(input).toHaveValue('/pi:summarize ')
    await user.clear(input)
    await user.type(input, '/rev second part')
    expect(screen.queryByRole('listbox')).toBeNull()
    await user.keyboard('{Home}{ArrowRight}{ArrowRight}')
    await waitFor(() => expect(labels()).toEqual(['/pi:review']))
    await user.click(options()[0])
    expect(input).toHaveValue('/pi:review second part')
    expect(input).toHaveFocus()
  })

  it('Esc closes the menu and Enter sends the text as typed; changing the first word opens it again', async () => {
    const { user, input, sent } = setup()
    await user.type(input, '/rev')
    await waitFor(() => expect(options()).toHaveLength(1))
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('listbox')).toBeNull()
    await user.type(input, 'i')
    await waitFor(() => expect(labels()).toEqual(['/pi:review']))
    await user.keyboard('{Escape}{Enter}')
    await waitFor(() => expect(sent).toEqual([{ text: '/revi' }]))
  })

  it('the Agent has never run (capturedAt is null): says the list appears after one run, Enter sends as usual', async () => {
    const { user, input, sent } = setup(() => HttpResponse.json(commandList()))
    await user.type(input, '/foo')
    expect(await screen.findByRole('status')).toHaveTextContent('The command list appears after the Agent has run once.')
    expect(screen.queryByRole('listbox')).toBeNull()
    await user.keyboard('{Enter}')
    await waitFor(() => expect(sent).toEqual([{ text: '/foo' }]))
  })

  it('an Agent without Pi (runtime is not pi): with no match it says the Agent has no Pi commands, not “after it has run once”', async () => {
    const { user, input } = setup(() => HttpResponse.json(commandList(platformCommands(), { runtime: 'claude-code' })))
    await user.type(input, '/review')
    expect(await screen.findByRole('status')).toHaveTextContent('No matching commands. This Agent runs on Claude Code and has no Pi commands.')
    expect(screen.getByRole('status')).not.toHaveTextContent('after the Agent has run once')
  })

  it('a Pi Agent whose commands were read but nothing matches: only says nothing matches', async () => {
    const { user, input } = setup()
    await user.type(input, '/zzz')
    expect(await screen.findByRole('status')).toHaveTextContent(/^No matching commands\.$/)
  })

  it('when the command list cannot be read it says why and does not block sending', async () => {
    const { user, input, sent } = setup(() => HttpResponse.json(errorBody('internal_error', 'boom'), { status: 500 }))
    await user.type(input, '/pi:review')
    expect(await screen.findByRole('status')).toHaveTextContent(/^Couldn't load the command list: Internal server error/)
    await user.keyboard('{Enter}')
    await waitFor(() => expect(sent).toEqual([{ text: '/pi:review' }]))
  })

  it('a Claude Code Agent: no /pi: commands; usable platform commands are suggested as usual, /compact, /export and /resume are listed under “Not available now” with the reason', async () => {
    const { user, input, sent } = setup(() => HttpResponse.json(claudeCodeList()))
    await user.type(input, '/')
    await waitFor(() => expect(labels()).toEqual(['/session', '/new', '/reload', '/copy']))
    const blocked = screen.getByRole('group', { name: 'Not available now' })
    expect([...blocked.querySelectorAll('code')].map((code) => code.textContent)).toEqual(['/compact [instructions]', '/export [html|jsonl]', '/resume [session-id]'])
    expect(within(blocked).getAllByText('This Agent runs on Claude Code and has no Pi session')).toHaveLength(3)
    expect(within(blocked).queryByRole('option')).toBeNull()
    // When no command matches, it only says so; it does not say “The command list appears after the Agent has run once”,
    // because this Agent has no Pi and would not get a list by running either.
    await user.clear(input)
    await user.type(input, '/review')
    expect(await screen.findByRole('status')).toHaveTextContent('No matching commands.')
    expect(screen.queryByRole('listbox')).toBeNull()
    await user.keyboard('{Enter}')
    await waitFor(() => expect(sent).toEqual([{ text: '/review' }]))
  })

  it('platform command descriptions use the server\'s Chinese and English text, picked by UI language; filtering uses that text too', async () => {
    const [session] = platformCommands()
    const ping = { ...session, name: 'ping', usage: '/ping [host]', description: { 'zh-CN': '测一下网络', 'en-US': 'Check the network' } }
    const { user, input } = setup(() => HttpResponse.json(commandList([session, ping])))
    await user.type(input, '/network')
    await waitFor(() => expect(labels()).toEqual(['/ping [host]']))
    expect(options()[0]).toHaveTextContent('/ping [host]Platform commandCheck the network')
    expect(options()[0]).not.toHaveTextContent('测一下网络') // check-english: zh (the zh-CN half of the two-language description must not show)
  })

  it('built-in commands follow only the server\'s list: unlisted ones are not shown, extra ones are shown as is (no description, none written)', async () => {
    const [session] = platformCommands()
    const ping = { ...session, name: 'ping', usage: '/ping [host]', description: null }
    const { user, input } = setup(() => HttpResponse.json(commandList([session, ping])))
    await user.type(input, '/')
    await waitFor(() => expect(labels()).toEqual(['/session', '/ping [host]']))
    expect(options()[0]).toHaveTextContent('/sessionPlatform commandShow the current session, model, and process')
    // When the server gives no description (null), none is shown, and the console does not fill one in with its own text.
    expect(options()[1]).toHaveTextContent(/^\/ping \[host\]Platform command$/)
    await user.keyboard('{ArrowDown}{Enter}')
    expect(input).toHaveValue('/ping')
  })

  it('unavailable commands are grayed out below with the reason and cannot be chosen; arrow keys and Enter move only among available commands', async () => {
    const commands = platformCommands({ compact: { available: false, unavailableReason: 'session_none' }, export: { available: false, unavailableReason: 'session_missing' } })
    const { user, input } = setup(() => HttpResponse.json(commandList(commands)))
    await user.type(input, '/')
    await waitFor(() => expect(labels()).toEqual(['/session', '/new', '/reload', '/copy', '/resume [session-id]']))
    const blocked = screen.getByRole('group', { name: 'Not available now' })
    expect(blocked).toHaveTextContent('/compact [instructions]Platform commandNo session yet. Send a message first')
    expect(blocked).toHaveTextContent('/export [html|jsonl]Platform commandThe session file is missing. Run /new or /resume first')
    for (const item of blocked.querySelectorAll('li')) expect(item).toHaveClass('text-ink-500')
    // Clicking an unavailable command does not fill it into the input.
    await user.click(within(blocked).getByText('/compact [instructions]'))
    expect(input).toHaveValue('/')
    await user.keyboard('{ArrowDown}{ArrowDown}')
    expect(options()[2]).toHaveAttribute('aria-selected', 'true')
    expect(labels()[2]).toBe('/reload')
    // When only unavailable commands match, there is nothing to choose; Enter sends as usual and the server explains why it won't work.
    await user.clear(input)
    await user.type(input, '/comp')
    await waitFor(() => expect(screen.queryByRole('listbox')).toBeNull())
    expect(screen.getByRole('group', { name: 'Not available now' })).toHaveTextContent('/compact [instructions]')
    expect(input).not.toHaveAttribute('aria-activedescendant')    // No command can be chosen: the footer no longer says “↑↓ to choose · Enter or Tab to fill in” and instead tells the person that Enter sends the text as typed; screen readers read it too.
    expect(screen.queryByText('↑↓ to choose · Enter or Tab to fill in · Esc to close')).toBeNull()
    expect(screen.getByRole('status')).toHaveTextContent('None of the matching commands can be used now; Enter sends the text as typed')
  })

  it('only a leading "/" counts: no menu with a space before it, when not at the start, or when the caret leaves the first word', async () => {
    const { user, input } = setup()
    await user.type(input, ' /pi:review')
    expect(screen.queryByRole('listbox')).toBeNull()
    await user.clear(input)
    await user.type(input, 'see /pi:review')
    expect(screen.queryByRole('listbox')).toBeNull()
    await user.clear(input)
    await user.type(input, '/pi:review src')
    expect(screen.queryByRole('listbox')).toBeNull()
    expect(screen.queryByRole('status')).toBeNull()
  })
})

describe('completion rules', () => {
  it('slashToken: the leading "/" word, with the caret inside it or right after it', () => {
    expect(slashToken('/pi:re', 6)).toBe('/pi:re')
    expect(slashToken('/pi:re arg', 3)).toBe('/pi:re')
    expect(slashToken('/pi:re arg', 7)).toBeNull()
    expect(slashToken(' /pi', 4)).toBeNull()
    expect(slashToken('', 0)).toBeNull()
  })

  it('applyCommand: replaces the first word, keeps the whitespace and text after it; adds a space when no whitespace follows', () => {
    expect(applyCommand('/re', command('review'))).toEqual({ text: '/pi:review ', caret: 11 })
    expect(applyCommand('/re\nsecond line', command('review'))).toEqual({ text: '/pi:review\nsecond line', caret: 11 })
  })

  it('commandText: Pi commands are written /pi:<name>; other sources (such as platform commands) /<name>, and completion and filtering follow', () => {
    expect(commandText(command('skill:loopctl'))).toBe('/pi:skill:loopctl')
    // Platform commands are merged in the frontend with the Pi commands returned by the server.
    const platform = { ...command('session'), source: 'platform' as const, usage: '/session' }
    expect(commandText(platform)).toBe('/session')
    expect(applyCommand('/se', platform)).toEqual({ text: '/session ', caret: 9 })
    expect(matchCommands([command('session'), platform], '/se')).toEqual([platform, command('session')])
  })

  it('matchCommands: case-insensitive, keeps the original order within a tier', () => {
    expect(matchCommands(list.commands, '/PI:').map((item) => item.name)).toEqual(['review', 'skill:loopctl', 'summarize'])
    expect(matchCommands(list.commands, '/loop').map((item) => item.name)).toEqual(['skill:loopctl'])
  })
})
