import { useEffect, useState } from 'react'
import { useApp } from '../context'
import { go } from '../lib/router'
import type { PlayerStats } from '../types'
import { CategoryBars } from '../components/SummaryView'
import { CONFIDENCE } from '../components/Game'

export function StatsScreen() {
  const { backend, identity, setIdentity, toast, cats } = useApp()
  const [stats, setStats] = useState<PlayerStats | null>(null)
  const [name, setName] = useState(identity?.name ?? '')
  useEffect(() => { backend.stats().then(setStats).catch((e) => toast(e.message)) }, [backend, toast])
  const save = async () => {
    try {
      setIdentity(await backend.rename(name))
      toast('Name saved')
    } catch (e) {
      toast((e as Error).message)
    }
  }
  return (
    <main className="stack">
      <h2>Your intuition</h2>
      <section className="panel tight stack">
        <label htmlFor="name" className="faint">Display name</label>
        <div className="row">
          <input id="name" className="name-input" style={{ flex: 1 }} value={name} maxLength={24} onChange={(e) => setName(e.target.value)} />
          <button className="btn" onClick={save} disabled={!name.trim() || name === identity?.name}>Save name</button>
        </div>
      </section>
      {!stats ? (
        <p className="faint">Loading…</p>
      ) : stats.answered === 0 ? (
        <section className="panel stack">
          <p>No answers yet. Your accuracy, streaks and surprise rate will build up here as you play.</p>
          <button className="btn primary" onClick={() => go('/play/quick')}>Play a quick round</button>
        </section>
      ) : (
        <>
          <div className="stat-grid">
            <div className="stat"><b>{stats.answered}</b><span>Questions answered</span></div>
            <div className="stat"><b>{Math.round((stats.accuracy ?? 0) * 100)}%</b><span>Accuracy</span></div>
            <div className="stat"><b>🔥 {stats.bestStreak}</b><span>Best streak</span></div>
            <div className="stat"><b>{stats.conceptsMastered}/{stats.conceptsSeen}</b><span>Concepts mastered</span></div>
            {stats.surpriseRate !== undefined && <div className="stat surprise"><b>{Math.round(stats.surpriseRate * 100)}%</b><span>Surprise rate</span></div>}
          </div>
          <section className="panel tight stack">
            <h3>By category</h3>
            <CategoryBars data={stats.byCategory} cats={cats} />
          </section>
          {Object.keys(stats.calibration).some((k) => stats.calibration[k].total > 0) && (
            <section className="panel tight stack">
              <h3>How reliable is your confidence?</h3>
              {CONFIDENCE.map((c) => {
                const t = stats.calibration[String(c.value)]
                if (!t?.total) return null
                return (
                  <p key={c.value}>
                    {c.emoji} When <b>{c.label.toLowerCase()}</b>, you were right {Math.round((t.correct / t.total) * 100)}% of the time ({t.total} answers).
                  </p>
                )
              })}
            </section>
          )}
        </>
      )}
    </main>
  )
}
