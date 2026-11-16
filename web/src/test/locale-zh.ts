// The Chinese supplement (src/zh-CN/, see its README.md) renders the console in the Chinese UI: the browser language is
// set to Chinese here, before any module reads the UI language. vitest.config.ts runs these files in the "zh-CN" project.
Object.defineProperty(window.navigator, 'language', { value: 'zh-CN', configurable: true })
Object.defineProperty(window.navigator, 'languages', { value: ['zh-CN'], configurable: true })
