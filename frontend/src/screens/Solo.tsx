import { useCallback, useEffect, useRef, useState } from 'react'
import { useApp } from '../context'
import { go } from '../lib/router'
import { play } from '../lib/sound'
import { useReducedMotion } from '../lib/share'
import { load, save } from '../lib/storage'
import type { AnswerResponse, Mode, QuestionView, SessionState, Summary } from '../types'
import { Progress, QuestionCard, RevealPanel, useAnswerKeys, useEnterKey } from '../components/Game'
import { SummaryView } from '../components/SummaryView'
import { ApiError } from '../backend'

interface Prefs { categories: string[]; confidence: boolean }

export function SoloSetup() {
  const { cats } = useApp()
  const [prefs, setPrefs] = useState<Prefs>(load('intuition.prefs', { categories: [], confidence: false }))
  const update = (p: Prefs) => { setPrefs(p); save('intuition.prefs', p) }
  const toggle = (id: string) =>
    update({ ...prefs, categories: prefs.categories.includes(id) ? prefs.categories.filter((c) => c !== id) : [...prefs.categories, id] })
  return (
    <main className="stack">
      <h2>Set up a solo game</h2>
      <section className="panel stack">
        <h3>Topics</h3>
        <p className="faint">Leave everything off for a mix of every domain.</p>
        <div className="cat-chips">
          {cats.map((c) => (
            <button key={c.id} className="cat-chip" aria-pressed={prefs.categories.includes(c.id)} style={{ ['--cat' as string]: c.color }} onClick={() => toggle(c.id)}>
              {c.emoji} {c.name} <span className="faint">{c.count}</span>
            </button>
          ))}
        </div>
        <label className="toggle">
          <input type="checkbox" checked={prefs.confidence} onChange={(e) => update({ ...prefs, confidence: e.target.checked })} />
          <span>
            <b>Confidence mode.</b> <span className="muted">Say how sure you are before each reveal, and track when you're confidently wrong.</span>
          </span>
        </label>
      </section>
      <div className="modes">
        {(['quick', 'standard', 'marathon'] as Mode[]).map((m) => (
          <button key={m} className={'mode' + (m === 'standard' ? ' recommended' : '')} onClick={() => go('/play/' + m)}>
            <b>{m[0].toUpperCase() + m.slice(1)}</b>
            <span>{{ quick: 5, standard: 10, marathon: 25 }[m]} questions</span>
          </button>
        ))}
      </div>
    </main>
  )
}

const pause = (ms: number) => new Promise((r) => setTimeout(r, ms))

export function SoloGame({ mode, resumeId, daily }: { mode?: Mode; resumeId?: string; daily?: boolean }) {
  const { backend, cats, toast } = useApp()
  const reduced = useReducedMotion()
  const [session, setSession] = useState<SessionState | null>(null)
  const [q, setQ] = useState<QuestionView | null>(null)
  const [results, setResults] = useState<(boolean | null)[]>([])
  const [streak, setStreak] = useState(0)
  const [pending, setPending] = useState<number | null>(null)
  const [picked, setPicked] = useState<number | null>(null)
  const [measuring, setMeasuring] = useState(false)
  const [resp, setResp] = useState<AnswerResponse | null>(null)
  const [summary, setSummary] = useState<Summary | null>(null)
  const [rank, setRank] = useState<number | undefined>()
  const [error, setError] = useState('')
  const busy = useRef(false)

  useEffect(() => {
    const prefs = load<Prefs>('intuition.prefs', { categories: [], confidence: false })
    const start = resumeId
      ? backend.getSession(resumeId)
      : daily
        ? backend.startDaily(prefs.confidence)
        : backend.startSession({ mode: mode ?? 'standard', categories: prefs.categories, confidence: prefs.confidence })
    start
      .then((s) => {
        setSession(s)
        setQ(s.question)
        setResults(Array.from({ length: s.total }, (_, i) => (i < s.answered ? null : null)))
        if (s.summary) setSummary(s.summary)
        if (!daily) history.replaceState(null, '', '#/session/' + s.sessionId)
      })
      .catch((e: ApiError) => {
        if (e.code === 'already_played') go('/daily')
        else setError(e.message)
      })
  }, [backend, mode, resumeId, daily])

  const submit = useCallback(
    async (choice: number, confidence: number) => {
      if (!session || !q || busy.current) return
      busy.current = true
      setPicked(choice)
      setPending(null)
      play('select')
      setMeasuring(!reduced)
      try {
        const [r] = await Promise.all([backend.answer(session.sessionId, q.id, choice, confidence), pause(reduced ? 0 : 750)])
        setMeasuring(false)
        setResp(r)
        setResults((prev) => prev.map((v, i) => (i === q.index ? r.result.correct : v)))
        setStreak(r.result.streak)
        play(r.result.correct ? 'correct' : 'wrong')
        if (r.dailyRank) setRank(r.dailyRank)
      } catch (e) {
        setMeasuring(false)
        setPicked(null)
        toast((e as Error).message)
      } finally {
        busy.current = false
      }
    },
    [session, q, backend, reduced, toast],
  )

  const pick = useCallback(
    (i: number) => {
      if (resp || picked !== null) return
      if (session?.confidence) { setPending(i); play('select') }
      else void submit(i, 0)
    },
    [resp, picked, session, submit],
  )

  const next = useCallback(() => {
    if (!resp) return
    if (resp.next) {
      setQ(resp.next)
      setResp(null)
      setPicked(null)
      setPending(null)
      window.scrollTo({ top: 0, behavior: reduced ? 'auto' : 'smooth' })
    } else if (resp.summary) {
      setSummary(resp.summary)
      play('victory')
      if (!daily) history.replaceState(null, '', '#/')
    }
  }, [resp, reduced, daily])

  useAnswerKeys(!!q && !resp && !summary && picked === null && pending === null, q?.options.length ?? 0, pick)
  useAnswerKeys(pending !== null, 3, (i) => submit(pending!, i + 1))
  useEnterKey(!!resp, next)

  if (error)
    return (
      <main className="stack">
        <div className="panel stack">
          <h2>That game couldn't start</h2>
          <p className="muted">{error}</p>
          <button className="btn primary" onClick={() => go('/')}>Back to home</button>
        </div>
      </main>
    )
  if (summary)
    return (
      <main>
        <SummaryView
          s={summary}
          cats={cats}
          label={daily ? `Daily ${session?.dailyDate ?? ''}` : undefined}
          extra={daily && rank ? <p className="banner">You're ranked {rank} on today's leaderboard.</p> : undefined}
        />
        <div className="continue-bar">
          {daily ? (
            <button className="btn primary" onClick={() => go('/daily')}>See today's leaderboard</button>
          ) : (
            <>
              <button className="btn" onClick={() => go('/')}>Home</button>
              <button className="btn primary" onClick={() => { go('/play/' + (session?.kind ?? 'standard') + '/' + Date.now().toString(36)) }}>Play again</button>
            </>
          )}
        </div>
      </main>
    )
  if (!q || !session) return <main><p className="faint" role="status">Choosing questions you haven't seen…</p></main>

  return (
    <main>
      <Progress index={q.index} total={session.total} results={results} streak={streak} />
      <QuestionCard
        q={q}
        state={{ picked, reveal: resp?.reveal, measuring }}
        locked={picked !== null}
        onPick={pick}
        confidenceMode={session.confidence}
        pendingChoice={pending}
        onConfidence={(c) => submit(pending!, c)}
      />
      {resp && (
        <>
          <RevealPanel reveal={resp.reveal} correct={resp.result.correct} answered />
          <div className="continue-bar">
            <button className="btn primary" onClick={next} autoFocus>
              {resp.next ? 'Next question' : 'See results'}
            </button>
          </div>
        </>
      )}
    </main>
  )
}
