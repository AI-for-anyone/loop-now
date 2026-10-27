import hljs from 'highlight.js/lib/core'
import bash from 'highlight.js/lib/languages/bash'
import css from 'highlight.js/lib/languages/css'
import go from 'highlight.js/lib/languages/go'
import ini from 'highlight.js/lib/languages/ini'
import javascript from 'highlight.js/lib/languages/javascript'
import json from 'highlight.js/lib/languages/json'
import markdown from 'highlight.js/lib/languages/markdown'
import python from 'highlight.js/lib/languages/python'
import sql from 'highlight.js/lib/languages/sql'
import typescript from 'highlight.js/lib/languages/typescript'
import xml from 'highlight.js/lib/languages/xml'
import yaml from 'highlight.js/lib/languages/yaml'

for (const [name, language] of Object.entries({ bash, css, go, ini, javascript, json, markdown, python, sql, typescript, xml, yaml })) hljs.registerLanguage(name, language)

const escape = (text: string) => text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

// 文件内容的高亮 HTML（highlight.js 会转义原文）；不认识的语言只转义，不高亮。
export function highlightCode(text: string, language: string | null): string {
  if (!language || !hljs.getLanguage(language)) return escape(text)
  return hljs.highlight(text, { language, ignoreIllegals: true }).value
}
