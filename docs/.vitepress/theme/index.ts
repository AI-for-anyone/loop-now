import DefaultTheme from 'vitepress/theme'
import { MermaidPlugin } from 'vitepress-plugin-mermaid'
import './custom.css'

export default {
  extends: DefaultTheme,
  enhanceApp({ app }) {
    app.use(MermaidPlugin)
  }
}
