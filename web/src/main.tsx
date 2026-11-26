import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './app/App'
import { t } from './i18n'
import { locale } from './i18n/locale'
import { initTheme } from './theme/store'
import './index.css'

// index.html is written in Chinese; rewrite it for the chosen UI language, since screen readers and browser translation use this to tell
// the page's language.
document.documentElement.lang = locale
document.title = t.app.title
initTheme()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
