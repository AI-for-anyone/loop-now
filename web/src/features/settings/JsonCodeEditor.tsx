import { useEffect, useRef } from 'react'
import { basicSetup } from 'codemirror'
import { json, jsonParseLinter } from '@codemirror/lang-json'
import { defaultHighlightStyle, HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { Compartment, EditorState, type Extension } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { tags } from '@lezer/highlight'
import { cn } from '@/lib/utils'
import { t } from '@/i18n'
import { useTheme } from '@/theme/store'

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
  fill?: boolean
  onChange: (value: string) => void
  onDiagnosticsChange: (diagnostics: JsonDiagnostic[]) => void
  diagnostics: JsonDiagnostic[]
}

const darkHighlightStyle = HighlightStyle.define([
  { tag: tags.propertyName, color: '#FFFFFF', fontWeight: '600' },
  { tag: tags.string, color: '#D4D4D8' },
  { tag: tags.number, color: '#4ADE80' },
  { tag: [tags.bool, tags.null, tags.keyword], color: '#93C5FD', fontWeight: '600' },
  { tag: [tags.punctuation, tags.bracket, tags.brace, tags.squareBracket], color: '#A3A3A3' },
  { tag: tags.comment, color: '#8F8F8F', fontStyle: 'italic' },
  { tag: tags.invalid, color: '#F87171' },
])

function buildEditorTheme(dark: boolean): Extension {
  return [
    EditorView.theme(
      {
        '&': { height: '100%', fontSize: '13px', backgroundColor: 'transparent', color: 'var(--ink)' },
        '.cm-scroller': { fontFamily: 'var(--font-mono)', lineHeight: '1.65', overflow: 'auto' },
        '.cm-content': { padding: '14px 0', caretColor: 'var(--ink)' },
        '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--ink)' },
        '.cm-line': { padding: '0 14px 0 8px' },
        '.cm-gutters': { backgroundColor: 'var(--surface-subtle)', color: 'var(--ink-500)', border: 'none', borderRight: '1px solid var(--hairline)', paddingLeft: '4px' },
        '.cm-activeLine': { backgroundColor: 'var(--surface-hover)' },
        '.cm-activeLineGutter': { backgroundColor: 'var(--surface-hover)' },
        '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection': {
          backgroundColor: dark ? 'rgb(255 255 255 / 0.16)' : 'rgb(0 0 0 / 0.10)',
        },
        '.cm-foldGutter span': { padding: '0 4px', cursor: 'pointer' },
        '&.cm-focused': { outline: 'none' },
      },
      { dark },
    ),
    syntaxHighlighting(dark ? darkHighlightStyle : defaultHighlightStyle),
  ]
}

export function JsonCodeEditor({ id, label, value, disabled = false, fill = false, onChange, onDiagnosticsChange, diagnostics }: JsonCodeEditorProps) {
  const { resolvedTheme } = useTheme()
  const dark = resolvedTheme === 'dark'
  const hostRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)
  const callbacksRef = useRef({ onChange, onDiagnosticsChange })
  const valueRef = useRef(value)
  const darkRef = useRef(dark)
  const editableRef = useRef(new Compartment())
  const themeRef = useRef(new Compartment())

  useEffect(() => {
    callbacksRef.current = { onChange, onDiagnosticsChange }
    valueRef.current = value
    darkRef.current = dark
  }, [onChange, onDiagnosticsChange, value, dark])

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
          themeRef.current.of(buildEditorTheme(darkRef.current)),
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
    view.dispatch({ effects: themeRef.current.reconfigure(buildEditorTheme(dark)) })
  }, [dark])

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

  return <div className={fill ? 'flex min-h-0 min-w-0 flex-1 flex-col gap-2' : 'grid min-w-0 gap-2'}>
    <div ref={hostRef} className={cn(fill ? 'min-h-[380px] flex-1' : 'h-[clamp(360px,55vh,680px)]', 'min-w-0 overflow-hidden rounded-card border border-hairline-strong bg-surface-control transition-colors focus-within:border-ink/35', disabled && 'opacity-60')} />
    {diagnostics.length > 0 && <div className="grid shrink-0 gap-1.5 rounded-card bg-tint-red/45 px-3 py-2.5" role="alert" aria-label={t.settings.models.syntaxErrors}>
      <p className="text-[13px] font-black text-red-text">{t.settings.models.syntaxErrors}</p>
      {diagnostics.map((diagnostic, index) => <button key={`${diagnostic.from}-${index}`} type="button" className="w-fit rounded text-left text-[13px] font-bold text-red-text underline decoration-red/40 underline-offset-2 outline-none hover:decoration-red focus-visible:shadow-focus" onClick={() => locate(diagnostic)}>
        {t.settings.models.errorLocation(diagnostic.line, diagnostic.column)}{diagnostic.message}
      </button>)}
    </div>}
  </div>
}
