import { defineConfig } from 'vitepress'

const github = 'https://github.com/AI-for-anyone/loop-now'
const market = 'https://github.com/AI-for-anyone/loop-now-market'

export default defineConfig({
  base: '/loop-now/',
  cleanUrls: true,
  lastUpdated: true,
  ignoreDeadLinks: [/^http:\/\/localhost/],
  sitemap: {
    hostname: 'https://ai-for-anyone.github.io/loop-now'
  },
  // base is pinned to '/loop-now/' above, so the favicon href is absolute against it.
  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/loop-now/favicon.svg' }],
    ['meta', { name: 'theme-color', content: '#2443B5' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:site_name', content: 'Loop Now' }],
    ['meta', { property: 'og:image', content: 'https://ai-for-anyone.github.io/loop-now/og-console.png' }],
    ['meta', { name: 'twitter:card', content: 'summary_large_image' }]
  ],
  themeConfig: {
    logo: '/favicon.svg',
    search: { provider: 'local' },
    socialLinks: [{ icon: 'github', link: github }]
  },
  locales: {
    root: {
      label: 'English',
      lang: 'en',
      title: 'Loop Now',
      description: 'A local multi-agent workspace, built to extend with plugins.',
      head: [
        ['meta', { property: 'og:title', content: 'Loop Now — a local multi-agent workspace' }],
        ['meta', { property: 'og:description', content: 'Connect CLI and MCP tools, subscribe to events, and shape how your agents work — with plugins as the extension point.' }]
      ],
      themeConfig: {
        nav: [
          {
            text: 'Guide',
            items: [
              { text: 'Getting started', link: '/guide/getting-started' },
              { text: 'Concepts', link: '/guide/concepts' },
              { text: 'Architecture', link: '/guide/architecture' }
            ]
          },
          { text: 'Plugins', link: '/plugins/' },
          {
            text: 'Reference',
            items: [
              { text: 'Configuration', link: '/reference/configuration' },
              { text: 'Contributing', link: '/reference/contributing' }
            ]
          },
          { text: 'Market', link: market }
        ],
        sidebar: [
          {
            text: 'Guide',
            items: [
              { text: 'Getting started', link: '/guide/getting-started' },
              { text: 'Concepts', link: '/guide/concepts' },
              { text: 'Architecture', link: '/guide/architecture' }
            ]
          },
          {
            text: 'Plugins',
            items: [
              { text: 'Overview', link: '/plugins/' },
              { text: 'Specification', link: '/plugins/spec' },
              { text: 'Publishing to the market', link: '/plugins/publish' }
            ]
          },
          {
            text: 'Reference',
            items: [
              { text: 'Configuration', link: '/reference/configuration' },
              { text: 'Contributing', link: '/reference/contributing' }
            ]
          }
        ],
        editLink: {
          pattern: 'https://github.com/AI-for-anyone/loop-now/edit/main/docs/:path',
          text: 'Edit this page on GitHub'
        },
        outline: { level: [2, 3] },
        docFooter: { prev: 'Previous page', next: 'Next page' },
        footer: {
          message: 'Released under the Apache-2.0 License.',
          copyright: 'Copyright © AI-for-anyone'
        }
      }
    },
    zh: {
      label: '简体中文',
      lang: 'zh-CN',
      link: '/zh/',
      title: 'Loop Now',
      description: '让多个 AI Agent 在本地协作，用插件扩展它们的能力。',
      head: [
        ['meta', { property: 'og:title', content: 'Loop Now — 本地多 Agent 协作工作台' }],
        ['meta', { property: 'og:description', content: '接入 CLI 与 MCP，订阅事件、编写 Hook，用插件扩展团队的能力。' }]
      ],
      themeConfig: {
        nav: [
          {
            text: '指南',
            items: [
              { text: '快速上手', link: '/zh/guide/getting-started' },
              { text: '核心概念', link: '/zh/guide/concepts' },
              { text: '架构', link: '/zh/guide/architecture' }
            ]
          },
          { text: '插件', link: '/zh/plugins/' },
          {
            text: '参考',
            items: [
              { text: '配置项', link: '/zh/reference/configuration' },
              { text: '参与贡献（英文）', link: '/reference/contributing' }
            ]
          },
          { text: '插件市场', link: market }
        ],
        sidebar: [
          {
            text: '指南',
            items: [
              { text: '快速上手', link: '/zh/guide/getting-started' },
              { text: '核心概念', link: '/zh/guide/concepts' },
              { text: '架构', link: '/zh/guide/architecture' }
            ]
          },
          {
            text: '插件',
            items: [
              { text: '插件体系概览', link: '/zh/plugins/' },
              { text: '插件规范', link: '/zh/plugins/spec' },
              { text: '发布到插件市场', link: '/zh/plugins/publish' }
            ]
          },
          {
            text: '参考',
            items: [
              { text: '配置项', link: '/zh/reference/configuration' },
              { text: '参与贡献（英文）', link: '/reference/contributing' }
            ]
          }
        ],
        editLink: {
          pattern: 'https://github.com/AI-for-anyone/loop-now/edit/main/docs/:path',
          text: '在 GitHub 上编辑此页'
        },
        outline: { level: [2, 3] },
        docFooter: { prev: '上一页', next: '下一页' },
        footer: {
          message: '基于 Apache-2.0 许可证发布。',
          copyright: 'Copyright © AI-for-anyone'
        }
      }
    }
  }
})
