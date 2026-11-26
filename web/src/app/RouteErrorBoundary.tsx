import { isRouteErrorResponse, Link, useRouteError } from 'react-router'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'

// Recovery boundary for route rendering errors so desktop shell users (without a browser Back button) are never stranded.
export function RouteErrorBoundary() {
  const error = useRouteError()
  const detail = isRouteErrorResponse(error)
    ? `${error.status} ${error.statusText}`
    : error instanceof Error
      ? error.message
      : null

  return (
    <div className="flex min-h-screen items-center justify-center bg-surface-canvas p-6 text-ink">
      <div className="flex w-full max-w-[440px] flex-col gap-4 rounded-panel border border-hairline-strong bg-surface-elevated p-6 shadow-floating [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:shadow-print-dialog">
        <div className="grid gap-1.5">
          <h1 className="text-[18px] font-semibold text-ink">{t.shell.routeError.title}</h1>
          <p className="text-[13.5px] text-ink-700">{t.shell.routeError.hint}</p>
        </div>
        {detail && (
          <pre className="max-h-32 overflow-auto rounded-input border border-hairline bg-surface-subtle p-3 font-mono text-[12px] text-ink-700 whitespace-pre-wrap break-words">
            {detail}
          </pre>
        )}
        <div className="flex flex-wrap items-center gap-2.5">
          <Button asChild size="sm">
            <Link to="/">{t.shell.routeError.home}</Link>
          </Button>
          <Button type="button" variant="secondary" size="sm" onClick={() => window.location.reload()}>
            {t.shell.routeError.reload}
          </Button>
        </div>
      </div>
    </div>
  )
}
