import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { RuntimesCard } from '@/features/settings/RuntimesCard'
import { renderWithQuery } from '@/test/render'

describe('Agent runtimes in global settings (Chinese UI)', () => {
  it('the sentences about each runtime are joined without a space, as Chinese is written', async () => {
    renderWithQuery(<RuntimesCard />)
    await screen.findByRole('region', { name: 'Claude Code' })
    expect(screen.getByText(/随平台安装/)).toHaveTextContent(/^Pi 随平台安装。Claude Code 用本机安装的 claude 和你在本机的登录，平台不读取、不保存它的登录信息。$/)
  })
})
