// The repository web page of a plugin's git source.

// Only http(s) addresses have a web page (SSH forms like git@host:repo don't, and callers show them as plain text); the link never
// carries the account credentials contained in the address.
// GitHub addresses drop the trailing .git, and link to /tree/<ref> when a ref is given.
export function repositoryUrl(location: string, ref: string | null): string | null {
  let url: URL
  try {
    url = new URL(location)
  } catch {
    return null
  }
  if (url.protocol !== 'https:' && url.protocol !== 'http:') return null
  if (!/^(www\.)?github\.com$/i.test(url.hostname)) return url.origin + url.pathname
  const base = url.origin + url.pathname.replace(/\/+$/, '').replace(/\.git$/i, '')
  return ref ? `${base}/tree/${ref.split('/').map(encodeURIComponent).join('/')}` : base
}

// A market entry's source is written git:<address>[@ref] and split by the server's rules: the git address itself may contain @
// (git@host:repo),
// so only an @ after the last / and : counts as the ref. null when it isn't a git source.
export function marketGitSource(source: string): { location: string; ref: string | null } | null {
  if (!source.startsWith('git:')) return null
  const location = source.slice('git:'.length)
  const at = location.lastIndexOf('@')
  if (at > Math.max(location.lastIndexOf('/'), location.lastIndexOf(':'))) return { location: location.slice(0, at), ref: location.slice(at + 1) }
  return { location, ref: null }
}
