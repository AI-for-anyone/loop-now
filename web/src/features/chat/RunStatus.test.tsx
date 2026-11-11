import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { makeRun } from '@/test/api'
import { FailureNote, RunStatusBadge, ToolList } from './RunStatus'

describe('ToolList', () => {
  it('同一个工具调用的多次状态变化只显示最新的状态', () => {
    render(
      <ToolList
        tools={[
          { toolCallId: 'call_1', toolName: 'bash', state: 'started', at: '2026-10-12T00:00:00Z' },
          { toolCallId: 'call_2', toolName: 'read', state: 'started', at: '2026-10-12T00:00:01Z' },
          { toolCallId: 'call_1', toolName: 'bash', state: 'succeeded', at: '2026-10-12T00:00:02Z' },
          { toolCallId: 'call_2', toolName: 'read', state: 'failed', at: '2026-10-12T00:00:03Z' },
        ]}
      />,
    )
    const items = screen.getAllByRole('listitem').map((li) => li.textContent)
    expect(items).toEqual(['bash成功', 'read失败'])
  })

  it('进行中的工具图标旋转，减少动态效果时停止旋转并换成静态的“…”', () => {
    const { container, rerender } = render(
      <ToolList tools={[{ toolCallId: 'call_1', toolName: 'bash', state: 'started', at: '2026-10-12T00:00:00Z' }]} />,
    )
    const spinner = container.querySelector('[data-slot="tool-spinner"]')!
    expect(spinner.getAttribute('class')).toContain('riso-spin')
    expect(spinner.getAttribute('class')).toContain('motion-reduce:animate-none')
    expect(spinner.getAttribute('class')).toContain('motion-reduce:hidden')
    const mark = container.querySelector('[data-slot="tool-static-mark"]')!
    expect(mark).toHaveTextContent('…')
    expect(mark.className).toContain('hidden')
    expect(mark.className).toContain('motion-reduce:inline')

    rerender(<ToolList tools={[{ toolCallId: 'call_1', toolName: 'bash', state: 'succeeded', at: '2026-10-12T00:00:01Z' }]} />)
    expect(container.querySelector('[data-slot="tool-spinner"]')).toBeNull()
    expect(container.querySelector('[data-slot="tool-static-mark"]')).toBeNull()
  })
})

describe('回复的结局印章', () => {
  it('已打断与服务端重启中断用描边印章，不用红色', () => {
    const { rerender } = render(<RunStatusBadge run={makeRun({ status: 'interrupted', endReason: 'user_interrupt' })} />)
    let stamp = screen.getByRole('status')
    expect(stamp).toHaveTextContent('已打断')
    expect(stamp).toHaveAttribute('data-variant', 'stamp')
    expect(stamp).toHaveAttribute('data-tone', 'interrupted')
    expect(stamp.className).not.toMatch(/destructive|red/)

    rerender(<RunStatusBadge run={makeRun({ status: 'interrupted', endReason: 'server_restart' })} />)
    stamp = screen.getByRole('status')
    expect(stamp).toHaveTextContent('服务端重启中断')
    expect(stamp).toHaveAttribute('data-variant', 'stamp')
    expect(stamp).toHaveAttribute('data-tone', 'restart')
    expect(stamp.className).not.toMatch(/destructive|red/)
  })

  it('失败用红色印章，另加浅红说明块写明原因', () => {
    const run = makeRun({ status: 'failed', errorCode: 'pi_exited', errorMessage: 'exit status 137' })
    render(
      <>
        <RunStatusBadge run={run} />
        <FailureNote run={run} />
      </>,
    )
    const [stamp, note] = screen.getAllByRole('status')
    expect(stamp).toHaveTextContent(/^失败$/)
    expect(stamp).toHaveAttribute('data-variant', 'stamp')
    expect(stamp).toHaveAttribute('data-tone', 'danger')
    expect(note).toHaveTextContent('失败：Agent 进程异常退出（exit status 137）')
  })

  it.each([
    ['runtime_unavailable', 'claude: command not found', '失败：本机没找到 claude：装好 Claude Code，并确认在终端里能运行 claude（claude: command not found）'],
    ['runtime_login_required', '未登录', '失败：本机的 Claude Code 没有登录：在本机终端运行 claude 并登录（未登录）'],
    ['runtime_start_failed', 'claude 2.1.0：exit status 1', '失败：Claude Code 没能启动（claude 2.1.0：exit status 1）'],
    ['runtime_exited', 'signal: killed', '失败：Claude Code 进程在这一轮中意外退出（signal: killed）'],
    ['runtime_error', 'usage limit reached', '失败：Claude Code 报告这一轮出错（usage limit reached）'],
  ] as const)('Claude Code 运行时的失败 %s：说明怎么处理或发生了什么，带上服务端的说明', (errorCode, errorMessage, text) => {
    render(<FailureNote run={makeRun({ status: 'failed', errorCode, errorMessage })} />)
    expect(screen.getByRole('status')).toHaveTextContent(text)
  })

  it('正常完成不显示标记，也没有说明块', () => {
    const run = makeRun({ status: 'completed' })
    const { container } = render(
      <>
        <RunStatusBadge run={run} />
        <FailureNote run={run} />
      </>,
    )
    expect(container).toBeEmptyDOMElement()
  })

  it('进行中只显示静态的阶段文字，不带旋转图标', () => {
    const { container } = render(<RunStatusBadge run={makeRun({ status: 'starting', phase: 'waiting_slot' })} />)
    expect(screen.getByRole('status')).toHaveTextContent('等待执行名额')
    expect(container.querySelector('svg')).toBeNull()
    expect(container.innerHTML).not.toMatch(/animate-|riso-spin/)
  })
})
