// 作用域的图标：项目是文件夹，平台是中心圆加四条线。只作装饰。
export function ScopeIcon({ kind, className = 'size-4' }: { kind: 'project' | 'platform'; className?: string }) {
  return kind === 'platform' ? (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.4} strokeLinecap="round" aria-hidden="true" className={className}>
      <circle cx="12" cy="12" r="3.2" />
      <path d="M12 2.5v5M12 16.5v5M2.5 12h5M16.5 12h5" />
    </svg>
  ) : (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.4} strokeLinejoin="round" aria-hidden="true" className={className}>
      <path d="M3 6.5h6.5l2 2.5H21v10H3z" />
    </svg>
  )
}
