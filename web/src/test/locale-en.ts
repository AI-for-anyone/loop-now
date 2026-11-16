// Tests render the console in the English UI by default: the browser language is set to English here, before any module
// reads the UI language (jsdom's default is en-US as well; this makes it explicit). The Chinese UI is covered by the
// supplement in src/zh-CN/, which vitest.config.ts runs with locale-zh.ts instead.
Object.defineProperty(window.navigator, 'language', { value: 'en-US', configurable: true })
Object.defineProperty(window.navigator, 'languages', { value: ['en-US'], configurable: true })
