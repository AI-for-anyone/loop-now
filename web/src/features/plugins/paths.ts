// URLs of the plugin details: /plugins/<id> is the overview, and the other tabs are appended to it (/plugins/<id>/config, /plugins/<id>/activity).
export const pluginPath = (pluginId: string, tab?: string) => `/plugins/${encodeURIComponent(pluginId)}${tab ? `/${tab}` : ''}`

export const pluginActivityPath = (pluginId: string, view?: 'events' | 'records' | 'logs') =>
  `${pluginPath(pluginId, 'activity')}${view && view !== 'events' ? `?view=${view}` : ''}`
