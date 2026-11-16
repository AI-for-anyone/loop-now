// Tests assert against the Chinese UI by default: jsdom's browser language is en-US, so it is switched to Chinese here before any module
// reads the UI language. Test files for the English UI use @vitest-environment-options to set the address to one with ?lang=en-US.
Object.defineProperty(window.navigator, 'language', { value: 'zh-CN', configurable: true })
Object.defineProperty(window.navigator, 'languages', { value: ['zh-CN'], configurable: true })
