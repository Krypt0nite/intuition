import { useEffect, useState } from 'react'
import { useApp } from '../context'
import { go } from '../lib/router'
import type { DailyInfo } from '../types'

function useCountdown(target?: number) {
  const [now, setNow] = useState(Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [])
  if (!target) return ''
  const s = Math.max(0, Math.floor((target - now) / 1000))
  return `${Math.floor(s / 3600)}h ${String(Math.floor((s % 3600) / 60)).padStart(2, '0')}m`
}

export function Home() {
  const { backend } = useApp()
  const [daily, setDaily] = useState<DailyInfo | null>(null)
  useEffect(() => { backend.daily().then(setDaily).catch(() => {}) }, [backend])
  const countdown = useCountdown(daily?.nextResetAt)
  return (
    <main>
      <section className="hero">
        <h1>What happens next?</h1>
        <svg className="trace" viewBox="0 0 600 40" preserveAspectRatio="none" aria-hidden="true">
          <path d="M0 20 H180 C200 20 205 4 220 4 S240 36 255 36 S275 8 290 20 H600" fill="none" stroke="currentColor" strokeWidth="2" />
        </svg>
        <p className="lede">A situation. A prediction. Then the reveal, and the reason your intuition did or didn't see it coming.</p>
      </section>

      <section className="panel daily-card" aria-labelledby="daily-h">
        <div className="meta">
          <span>Same five questions for everyone today</span>
          {countdown && <span>New set in {countdown}</span>}
        </div>
        <h2 id="daily-h">Today's intuition</h2>
        {daily?.played ? (
          <p className="muted">
            You scored {daily.result?.score}/{daily.result?.total}
            {daily.rank ? `, ranked ${daily.rank} of ${daily.totalPlayers}` : ''}. Come back tomorrow for a new set.
          </p>
        ) : (
          <p className="muted">One attempt. {daily?.totalPlayers ? `${daily.totalPlayers} players so far today.` : 'Be the first today.'}</p>
        )}
        <button className="btn primary block" onClick={() => go('/daily')}>
          {daily?.played ? 'See today’s leaderboard' : daily?.inProgress ? 'Resume today’s challenge' : 'Play today’s challenge'}
        </button>
      </section>

      <div className="section-title">
        <h3>Solo</h3>
        <button className="chip-btn" onClick={() => go('/play')}>Choose topics</button>
      </div>
      <div className="modes">
        <button className="mode" onClick={() => go('/play/quick')}><b>Quick</b><span>5 questions, about 2 minutes</span></button>
        <button className="mode recommended" onClick={() => go('/play/standard')}><b>Standard</b><span>10 questions, recommended</span></button>
        <button className="mode" onClick={() => go('/play/marathon')}><b>Marathon</b><span>25 questions, settle in</span></button>
      </div>

      <div className="section-title"><h3>With friends</h3></div>
      <section className="panel tight row" style={{ justifyContent: 'space-between' }}>
        <p className="muted" style={{ flex: '1 1 260px' }}>
          Everyone gets the same question. Answers lock in, then the reveal hits all at once.
        </p>
        <button className="btn" onClick={() => go('/room')} disabled={backend.kind === 'demo'}>
          {backend.kind === 'demo' ? 'Needs the game server' : 'Create or join a room'}
        </button>
      </section>
    </main>
  )
}
