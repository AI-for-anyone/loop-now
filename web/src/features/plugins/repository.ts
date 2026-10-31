// 插件 git 来源的仓库网页。

// 只有 http(s) 地址有网页（git@host:repo 这类 SSH 写法没有，调用方只显示文字）；链接里不带地址中的账号口令。
// GitHub 地址去掉结尾的 .git，给了 ref 时链到 /tree/<ref>。
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

// 市场条目的来源写法 git:<地址>[@ref]，按服务端的规则拆：git 地址本身可能含 @（git@host:repo），
// 只把最后一个 / 与 : 之后的 @ 视为 ref。不是 git 来源时为 null。
export function marketGitSource(source: string): { location: string; ref: string | null } | null {
  if (!source.startsWith('git:')) return null
  const location = source.slice('git:'.length)
  const at = location.lastIndexOf('@')
  if (at > Math.max(location.lastIndexOf('/'), location.lastIndexOf(':'))) return { location: location.slice(0, at), ref: location.slice(at + 1) }
  return { location, ref: null }
}
