import { useEffect, useState } from 'react'
import { useApp } from '../context'
import { go } from '../lib/router'
import type { DailyInfo } from '../types'
import { SoloGame } from './Solo'

export function DailyScreen() {
  const { backend, identity } = useApp()
  const [info, setInfo] = useState<DailyInfo | null>(null)
  const [playing, setPlaying] = useState(false)
  useEffect(() => { if (!playing) backend.daily().then(setInfo).catch(() => {}) }, [backend, playing])
  if (playing) return <SoloGame daily />
  if (!info) return <main><p className="faint" role="status">Loading today's challenge…</p></main>
  return (
    <main className="stack">
      <div>
        <p className="faint">{new Date(info.date + 'T12:00:00').toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'long' })}</p>
        <h2>Today's intuition</h2>
      </div>
      {!info.played ? (
        <section className="panel stack">
          <p>Five questions, one from each of five different domains, easy to expert. Everyone in the world gets the same set and the same answer order. You get one attempt.</p>
          <p className="faint">Ties on score are broken by total answer time.</p>
          <button className="btn primary block" onClick={() => setPlaying(true)}>{info.inProgress ? 'Resume' : 'Start'}</button>
        </section>
      ) : (
        <section className="panel stack">
          <h3>
            You scored {info.result?.score}/{info.result?.total}
            {info.rank ? `, ranked ${info.rank} of ${info.totalPlayers}` : ''}
          </h3>
          <p className="muted">A new set arrives at midnight. In the meantime, a solo round never repeats what you've just seen.</p>
          <button className="btn" onClick={() => go('/play/standard')}>Play a solo round</button>
        </section>
      )}
      <section className="panel tight stack">
        <h3>Leaderboard</h3>
        {info.leaderboard.length === 0 ? (
          <p className="faint">Nobody has finished today's set yet.</p>
        ) : (
          <ol className="players">
            {info.leaderboard.map((e, i) => (
              <li key={e.playerId} className={e.playerId === identity?.playerId ? 'me' : ''}>
                <span className="avatar" style={{ background: i === 0 ? 'var(--b)' : 'var(--panel)', color: i === 0 ? '#0a0f1f' : 'var(--ink)' }}>{i + 1}</span>
                <span>{e.playerName}</span>
                <span className="tag">
                  {e.score}/{e.total} · {(e.totalMs / 1000).toFixed(1)}s
                </span>
              </li>
            ))}
          </ol>
        )}
      </section>
    </main>
  )
}
