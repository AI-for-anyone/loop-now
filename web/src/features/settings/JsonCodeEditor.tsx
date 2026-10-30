import { useEffect, useRef } from 'react'
import { basicSetup } from 'codemirror'
import { json, jsonParseLinter } from '@codemirror/lang-json'
import { Compartment, EditorState } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { cn } from '@/lib/utils'
import { t } from '@/i18n'

export interface JsonDiagnostic {
  from: number
  to: number
  line: number
  column: number
  message: string
}

interface JsonCodeEditorProps {
  id: string
  label: string
  value: string
  disabled?: boolean
  onChange: (value: string) => void
  onDiagnosticsChange: (diagnostics: JsonDiagnostic[]) => void
  diagnostics: JsonDiagnostic[]
}

export function JsonCodeEditor({ id, label, value, disabled = false, onChange, onDiagnosticsChange, diagnostics }: JsonCodeEditorProps) {
  const hostRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)
  const callbacksRef = useRef({ onChange, onDiagnosticsChange })
  const valueRef = useRef(value)
  const editableRef = useRef(new Compartment())

  useEffect(() => {
    callbacksRef.current = { onChange, onDiagnosticsChange }
    valueRef.current = value
  }, [onChange, onDiagnosticsChange, value])

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    const report = (view: EditorView) => {
      const diagnostics = jsonParseLinter()(view).map(diagnostic => {
        const line = view.state.doc.lineAt(diagnostic.from)
        return { from: diagnostic.from, to: diagnostic.to, line: line.number, column: diagnostic.from - line.from + 1, message: diagnostic.message }
      })
      callbacksRef.current.onDiagnosticsChange(diagnostics)
      view.contentDOM.setAttribute('aria-invalid', diagnostics.length > 0 ? 'true' : 'false')
    }
    const view = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: valueRef.current,
        extensions: [
          basicSetup,
          json(),
          EditorView.lineWrapping,
          editableRef.current.of(EditorView.editable.of(true)),
          EditorView.theme({
            '&': { height: '100%', fontSize: '13px', backgroundColor: 'transparent' },
            '.cm-scroller': { fontFamily: 'var(--font-mono)', lineHeight: '1.65', overflow: 'auto' },
            '.cm-content': { padding: '14px 0', caretColor: 'var(--ink)' },
            '.cm-line': { padding: '0 14px 0 8px' },
            '.cm-gutters': { backgroundColor: 'var(--paper-raised)', color: 'var(--ink-500)', border: 'none', paddingLeft: '4px' },
            '.cm-activeLine': { backgroundColor: 'var(--tint-blue)' },
            '.cm-activeLineGutter': { backgroundColor: 'var(--tint-blue)' },
            '.cm-foldGutter span': { padding: '0 4px', cursor: 'pointer' },
            '&.cm-focused': { outline: 'none' },
          }),
          EditorView.updateListener.of(update => {
            if (update.docChanged) {
              const next = update.state.doc.toString()
              if (next !== valueRef.current) callbacksRef.current.onChange(next)
              report(update.view)
            }
          }),
        ],
      }),
    })
    view.contentDOM.id = id
    view.contentDOM.setAttribute('aria-label', label)
    view.contentDOM.setAttribute('aria-multiline', 'true')
    viewRef.current = view
    report(view)
    return () => {
      view.destroy()
      viewRef.current = null
    }
  }, [id, label])

  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    view.dispatch({ effects: editableRef.current.reconfigure(EditorView.editable.of(!disabled)) })
    view.contentDOM.setAttribute('aria-disabled', disabled ? 'true' : 'false')
  }, [disabled])

  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const current = view.state.doc.toString()
    if (current !== value) view.dispatch({ changes: { from: 0, to: current.length, insert: value } })
  }, [value])

  const locate = (diagnostic: JsonDiagnostic) => {
    const view = viewRef.current
    if (!view) return
    view.dispatch({ selection: { anchor: diagnostic.from, head: diagnostic.to }, scrollIntoView: true })
    view.focus()
  }

  return <div className="grid min-w-0 gap-2">
    <div ref={hostRef} className={cn('h-[clamp(360px,55vh,680px)] min-w-0 overflow-hidden rounded-card border border-ink/15 bg-white shadow-inner focus-within:border-riso-blue focus-within:shadow-focus', disabled && 'opacity-60')} />
    {diagnostics.length > 0 && <div className="grid gap-1.5 rounded-card bg-tint-red/45 px-3 py-2.5" role="alert" aria-label={t.settings.models.syntaxErrors}>
      <p className="text-[13px] font-black text-red-text">{t.settings.models.syntaxErrors}</p>
      {diagnostics.map((diagnostic, index) => <button key={`${diagnostic.from}-${index}`} type="button" className="w-fit rounded text-left text-[13px] font-bold text-red-text underline decoration-red/40 underline-offset-2 outline-none hover:decoration-red focus-visible:shadow-focus" onClick={() => locate(diagnostic)}>
        {t.settings.models.errorLocation(diagnostic.line, diagnostic.column)}{diagnostic.message}
      </button>)}
    </div>}
  </div>
}
