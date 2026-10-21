import { useState } from 'react'
import type { CategoryInfo, Summary } from '../types'
import { shareOrCopy, shareText } from '../lib/share'

export function catName(cats: CategoryInfo[], id?: string) {
  const c = cats.find((x) => x.id === id)
  return c ? `${c.emoji} ${c.name}` : id ?? ''
}

export function CategoryBars({ data, cats }: { data: Record<string, { correct: number; total: number }>; cats: CategoryInfo[] }) {
  const rows = cats.filter((c) => data[c.id])
  if (!rows.length) return null
  return (
    <div className="bars">
      {rows.map((c) => {
        const t = data[c.id]
        return (
          <div className="bar-row" key={c.id} style={{ ['--cat' as string]: c.color }}>
            <span>
              {c.emoji} {c.name}
            </span>
            <span className="track" role="img" aria-label={`${t.correct} of ${t.total} correct`}>
              <i style={{ width: `${(t.correct / t.total) * 100}%` }} />
            </span>
            <span className="faint">
              {t.correct}/{t.total}
            </span>
          </div>
        )
      })}
    </div>
  )
}

export function SummaryView({ s, cats, label, extra }: { s: Summary; cats: CategoryInfo[]; label?: string; extra?: React.ReactNode }) {
  const [shareState, setShareState] = useState('')
  const text = shareText(s, cats, label)
  const verdict =
    s.reliability >= 90 ? 'Uncanny intuition.' : s.reliability >= 70 ? 'Mostly reliable intuition.' : s.reliability >= 40 ? 'Your intuition had some surprises.' : 'Your intuition got thoroughly ambushed.'
  return (
    <div className="stack enter-up">
      <div>
        <div className="score-big" aria-label={`${s.score} out of ${s.total}`}>
          {s.score}
          <small>/{s.total}</small>
        </div>
        <h2 style={{ marginTop: 10 }}>{verdict}</h2>
        <p className="muted">Your intuition was {s.reliability}% reliable this round.</p>
      </div>
      {extra}
      <div className="stat-grid">
        <div className="stat"><b>🔥 {s.bestStreak}</b><span>Best streak</span></div>
        <div className="stat"><b>{(s.avgResponseMs / 1000).toFixed(1)}s</b><span>Average answer time</span></div>
        {s.strongest && <div className="stat"><b>{catName(cats, s.strongest)}</b><span>Strongest area</span></div>}
        {s.weakest && <div className="stat"><b>{catName(cats, s.weakest)}</b><span>Needs work</span></div>}
        {s.confidentAnswers > 0 && (
          <div className="stat surprise"><b>{Math.round(s.surpriseRate * 100)}%</b><span>Surprise rate: confident but wrong</span></div>
        )}
      </div>
      {s.biggestSurprise && (
        <div className="panel tight surprise">
          <h3>{s.biggestSurprise.confidence === 3 ? 'You were absolutely certain…' : 'You were fairly sure…'}</h3>
          <p style={{ marginTop: 6 }}>
            “{s.biggestSurprise.question}” You said <b>{s.biggestSurprise.yourAnswer || 'nothing'}</b>. Actually: {s.biggestSurprise.reveal}
          </p>
        </div>
      )}
      <section className="panel tight stack">
        <h3>By category</h3>
        <CategoryBars data={s.byCategory} cats={cats} />
      </section>
      {s.missed.length > 0 && (
        <section className="panel tight stack">
          <h3>What surprised you</h3>
          <div className="missed">
            {s.missed.map((m) => (
              <div className="item" key={m.questionId}>
                <b>
                  {m.emoji} {m.question}
                </b>
                <p>
                  {m.reveal} {m.principle}
                </p>
              </div>
            ))}
          </div>
        </section>
      )}
      <section className="panel tight stack">
        <h3>Principles you met</h3>
        <ul style={{ margin: 0, paddingLeft: 20 }}>
          {s.principles.map((p) => (
            <li key={p} className="muted">{p}</li>
          ))}
        </ul>
      </section>
      <section className="panel tight stack share">
        <h3>Share your result</h3>
        <pre>{text}</pre>
        <button className="btn" onClick={async () => setShareState(await shareOrCopy(text))}>
          {shareState === 'copied' ? 'Copied' : shareState === 'shared' ? 'Shared' : 'Share result'}
        </button>
      </section>
    </div>
  )
}
