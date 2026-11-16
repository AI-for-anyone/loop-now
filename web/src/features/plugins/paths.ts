// URLs of the plugin details: /plugins/<id> is the overview, and the other tabs are appended to it (/plugins/<id>/config etc.).
export const pluginPath = (pluginId: string, tab?: string) => `/plugins/${encodeURIComponent(pluginId)}${tab ? `/${tab}` : ''}`
