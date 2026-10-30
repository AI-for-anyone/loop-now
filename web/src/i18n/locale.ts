// 控制台的界面语言。目前只有中文；以后按浏览器语言与全局设置选择。
export type Locale = 'zh-CN'

export const locale: Locale = 'zh-CN'

// 服务端返回的说明文字（错误说明、字段校验、执行失败原因）只有中文。界面不是中文时，
// 有错误码文案的地方只显示文案表里的说明，不再附上服务端原文；没有错误码文案的原样显示。
export const showServerText = locale === 'zh-CN'
