// 插件详情的地址：/plugins/<id> 为概览，其余页签接在后面（/plugins/<id>/config 等）。
export const pluginPath = (pluginId: string, tab?: string) => `/plugins/${encodeURIComponent(pluginId)}${tab ? `/${tab}` : ''}`
