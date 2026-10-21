import { useCallback, useEffect, useState } from 'react'
import { detectBackend, type Backend } from './backend'
import { Ctx } from './context'
import { go, useRoute } from './lib/router'
import { isMuted, onMuteChange, setMuted } from './lib/sound'
import type { CategoryInfo, Identity, Mode } from './types'
import { Home } from './screens/Home'
import { SoloSetup, SoloGame } from './screens/Solo'
import { DailyScreen } from './screens/Daily'
import { RoomEntry, RoomScreen } from './screens/Room'
import { StatsScreen } from './screens/Stats'

export default function App() {
  const [backend, setBackend] = useState<Backend | null>(null)
  const [error, setError] = useState('')
  const [identity, setIdentity] = useState<Identity | null>(null)
  const [cats, setCats] = useState<CategoryInfo[]>([])
  const [toastMsg, setToastMsg] = useState('')
  const [muted, setMutedState] = useState(isMuted())
  const route = useRoute()

  useEffect(() => onMuteChange(setMutedState), [])
  useEffect(() => {
    detectBackend()
      .then(async (b) => {
        setBackend(b)
        setIdentity(await b.ensurePlayer())
        setCats(await b.categories())
      })
      .catch((e) => setError(e.message))
  }, [])

  const toast = useCallback((m: string) => {
    setToastMsg(m)
    window.setTimeout(() => setToastMsg(''), 3200)
  }, [])

  if (error)
    return (
      <main>
        <div className="panel stack">
          <h2>Can't reach the game server</h2>
          <p className="muted">{error}</p>
          <button className="btn primary" onClick={() => location.reload()}>Try again</button>
        </div>
      </main>
    )
  if (!backend || !identity) return <main><p className="faint" role="status">Calibrating instruments…</p></main>

  const [page, arg] = route
  let screen: JSX.Element
  if (page === 'play' && arg) screen = <SoloGame key={route.join('/')} mode={arg as Mode} />
  else if (page === 'play') screen = <SoloSetup />
  else if (page === 'session' && arg) screen = <SoloGame key={arg} resumeId={arg} />
  else if (page === 'daily') screen = <DailyScreen />
  else if (page === 'room' && arg) screen = <RoomScreen key={arg} code={arg} />
  else if (page === 'room') screen = <RoomEntry />
  else if (page === 'stats') screen = <StatsScreen />
  else screen = <Home />

  return (
    <Ctx.Provider value={{ backend, identity, setIdentity, cats, toast }}>
      <div className="app">
        <header className="topbar">
          <button className="brand" onClick={() => go('/')} aria-label="Intuition home">
            <b>🔬 Intuition</b>
            <span>What happens next?</span>
          </button>
          <span className="spacer" />
          {backend.kind === 'demo' && <span className="mode-badge" title="Playing offline with the bundled question pack">Demo mode</span>}
          <button className="chip-btn" onClick={() => setMuted(!muted)} aria-pressed={!muted} aria-label={muted ? 'Sound off. Turn sound on' : 'Sound on. Turn sound off'}>
            {muted ? '🔇' : '🔊'}
          </button>
          <button className="chip-btn" onClick={() => go('/stats')} aria-label="Your profile and stats">
            {identity.name}
          </button>
        </header>
        {screen}
        {toastMsg && <div className="toast" role="status">{toastMsg}</div>}
      </div>
    </Ctx.Provider>
  )
}
