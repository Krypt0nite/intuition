import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import './styles.css'

// The full app defaults to the dark observatory look; the single-file demo
// follows the viewer's light/dark preference.
if (!__FORCE_DEMO__) document.documentElement.dataset.theme = 'dark'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
