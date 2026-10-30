// 测试默认按中文界面断言：jsdom 的浏览器语言是 en-US，这里在任何模块读取界面语言之前改成中文。
// 要测英文界面的用例文件用 @vitest-environment-options 把地址设成带 ?lang=en-US 的。
Object.defineProperty(window.navigator, 'language', { value: 'zh-CN', configurable: true })
Object.defineProperty(window.navigator, 'languages', { value: ['zh-CN'], configurable: true })
