import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './app/App'
import { t } from './i18n'
import { locale } from './i18n/locale'
import './index.css'

// index.html 里写的是中文；按选定的界面语言改写，读屏与浏览器翻译据此判断页面语言。
document.documentElement.lang = locale
document.title = t.app.title

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
