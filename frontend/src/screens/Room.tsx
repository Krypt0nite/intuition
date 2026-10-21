import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useApp } from '../context'
import { go } from '../lib/router'
import { play } from '../lib/sound'
import { RoomSocket, type ServerMsg, type Status } from '../net/socket'
import type { QuestionMsg, ResultsMsg, RevealMsg, RoomPlayer, RoomSettings, RoomState } from '../types'
import { QuestionCard, RevealPanel, TimerBar, useAnswerKeys } from '../components/Game'
import { SummaryView, catName } from '../components/SummaryView'

let shared: RoomSocket | null = null
function useSocket(): RoomSocket | null {
  const { backend, identity } = useApp()
  const url = backend.wsUrl()
  if (!shared && url && identity) shared = new RoomSocket(url, identity)
  return shared
}

const PALETTE = ['#6CB8FF', '#FFC94D', '#45D69A', '#FF6B6B', '#B79CFF', '#FF8FB1', '#5FD3E0', '#F2A65A']
const colorFor = (id: string) => PALETTE[[...id].reduce((a, c) => a + c.charCodeAt(0), 0) % PALETTE.length]

function Avatar({ p }: { p: RoomPlayer }) {
  return <span className="avatar" style={{ background: colorFor(p.id) }} aria-hidden="true">{p.name.slice(0, 1).toUpperCase()}</span>
}

export function RoomEntry() {
  const sock = useSocket()
  const { toast } = useApp()
  const [code, setCode] = useState('')
  const [creating, setCreating] = useState(false)
  const creatingRef = useRef(false)
  useEffect(() => {
    if (!sock) return
    return sock.onMessage((m) => {
      if (m.type === 'room_state' && creatingRef.current) { creatingRef.current = false; go('/room/' + m.data.code) }
      if (m.type === 'error') { creatingRef.current = false; setCreating(false); toast(m.data.message) }
    })
  }, [sock, toast])
  if (!sock) return <main><div className="panel">Multiplayer needs the game server. Start it with <code>make dev</code>.</div></main>
  const valid = /^[A-Z2-9]{5}$/.test(code)
  return (
    <main className="stack">
      <h2>Play with friends</h2>
      <section className="panel stack">
        <h3>Start a room</h3>
        <p className="muted">You'll get a five-letter code to share. You choose the topics and timer in the lobby.</p>
        <button className="btn primary block" disabled={creating} onClick={() => { creatingRef.current = true; setCreating(true); sock.send('create_room', {}) }}>
          {creating ? 'Creating…' : 'Create room'}
        </button>
      </section>
      <section className="panel stack">
        <h3>Join a room</h3>
        <form className="stack" onSubmit={(e) => { e.preventDefault(); if (valid) go('/room/' + code) }}>
          <label htmlFor="code" className="faint">Room code</label>
          <input id="code" className="join-input" autoComplete="off" autoCapitalize="characters" maxLength={5} value={code}
            onChange={(e) => setCode(e.target.value.toUpperCase().replace(/[^A-Z0-9]/g, ''))} placeholder="X7K9P" />
          <button className="btn block" disabled={!valid}>Join room</button>
        </form>
      </section>
    </main>
  )
}

export function RoomScreen({ code }: { code: string }) {
  const sock = useSocket()
  const { identity, toast } = useApp()
  const me = identity!.playerId
  const [state, setState] = useState<RoomState | null>(null)
  const [question, setQuestion] = useState<QuestionMsg | null>(null)
  const [myChoice, setMyChoice] = useState<number | null>(null)
  const [pending, setPending] = useState<number | null>(null)
  const [reveal, setReveal] = useState<RevealMsg | null>(null)
  const [results, setResults] = useState<ResultsMsg | null>(null)
  const [status, setStatus] = useState<Status>('connecting')
  const [fatal, setFatal] = useState('')
  const lastTick = useRef(0)

  useEffect(() => {
    if (!sock) return
    const offS = sock.onStatus(setStatus)
    setStatus(sock.status)
    const off = sock.onMessage((m: ServerMsg) => {
      switch (m.type) {
        case 'room_state': setState(m.data); if (m.data.phase === 'lobby') { setResults(null); setReveal(null); setQuestion(null) } break
        case 'question': setQuestion(m.data); setReveal(null); setMyChoice(null); setPending(null); break
        case 'answer_ack': setMyChoice(m.data.choice); setPending(null); break
        case 'reveal': {
          const r = m.data as RevealMsg
          setReveal(r)
          const mine = r.results.find((x) => x.playerId === me)
          play(mine?.correct ? 'correct' : 'wrong')
          break
        }
        case 'results': setResults(m.data); play('victory'); break
        case 'room_closed': setFatal('This room has closed.'); break
        case 'error':
          if (['room_not_found', 'room_full', 'game_in_progress', 'room_closed'].includes(m.data.code)) setFatal(m.data.message)
          else toast(m.data.message)
      }
    })
    // Joining is idempotent: an existing member just gets the current state again.
    sock.send('join_room', { code })
    return () => { off(); offS() }
  }, [sock, code, me, toast])

  const now = useCallback(() => sock?.serverNow() ?? Date.now(), [sock])
  const isHost = state?.hostId === me
  const answer = useCallback((choice: number, confidence: number) => {
    if (!question || myChoice !== null) return
    setMyChoice(choice)
    play('select')
    sock?.send('answer', { questionId: question.question.id, choice, confidence })
  }, [question, myChoice, sock])
  const pick = useCallback((i: number) => {
    if (question?.confidence) { setPending(i); play('select') } else answer(i, 0)
  }, [question, answer])
  const inQuestion = state?.phase === 'question' && !!question && !reveal
  useAnswerKeys(inQuestion && myChoice === null && pending === null, question?.question.options.length ?? 0, pick)

  // Countdown tick for the last five seconds.
  useEffect(() => {
    if (!inQuestion || myChoice !== null) return
    const t = setInterval(() => {
      const s = Math.ceil((question!.deadline - now()) / 1000)
      if (s <= 5 && s > 0 && s !== lastTick.current) { lastTick.current = s; play('tick') }
    }, 200)
    return () => clearInterval(t)
  }, [inQuestion, myChoice, question, now])

  const leave = () => { sock?.send('leave_room'); go('/') }

  if (fatal)
    return (
      <main className="stack">
        <div className="panel stack">
          <h2>Can't join {code}</h2>
          <p className="muted">{fatal}</p>
          <button className="btn primary" onClick={() => go('/room')}>Try another code</button>
        </div>
      </main>
    )
  if (!state) return <main><p className="faint" role="status">Joining room {code}…</p></main>

  const players = state.players
  const sidebar = (
    <aside className="panel tight stack" aria-label="Players">
      <div className="row" style={{ justifyContent: 'space-between' }}>
        <h3>Players</h3>
        <span className="faint">{players.filter((p) => p.connected).length} online</span>
      </div>
      <ul className="players">
        {[...players].sort((a, b) => (state.phase === 'lobby' ? 0 : b.score - a.score)).map((p) => (
          <li key={p.id} className={(p.id === me ? 'me ' : '') + (p.connected ? '' : 'off')}>
            <Avatar p={p} />
            <span>
              {p.name} {p.isHost && <span className="tag">host</span>} {!p.connected && <span className="tag">reconnecting…</span>}
            </span>
            <span>
              {state.phase === 'question' && (p.answered ? <span className="done" aria-label="answered">✓</span> : <span className="tag">thinking</span>)}
              {state.phase === 'reveal' && (p.ready ? <span className="done">ready</span> : <b>{p.score}</b>)}
              {(state.phase === 'results' || state.phase === 'lobby') && state.phase !== 'lobby' && <b>{p.score}</b>}
            </span>
          </li>
        ))}
      </ul>
      {status !== 'open' && <p className="banner warn" role="status">Connection lost, reconnecting…</p>}
      <button className="chip-btn" onClick={leave}>Leave room</button>
    </aside>
  )

  let body: JSX.Element
  if (state.phase === 'lobby') body = <Lobby state={state} isHost={isHost} send={(t, d) => sock?.send(t, d)} />
  else if (results && state.phase === 'results')
    body = <Results results={results} me={me} isHost={isHost} onAgain={() => sock?.send('play_again')} onHome={leave} />
  else if (question) {
    const myResult = reveal?.results.find((r) => r.playerId === me)
    const answeredCount = players.filter((p) => p.answered).length
    const connected = players.filter((p) => p.connected).length
    body = (
      <div>
        <div className="progress"><span>Question {question.question.index + 1} of {question.question.total}</span></div>
        {!reveal && <TimerBar deadline={question.deadline} duration={question.durationMs} now={now} />}
        <QuestionCard
          q={question.question}
          state={{ picked: myChoice, reveal: reveal?.reveal, distribution: reveal?.distribution }}
          locked={myChoice !== null || !!reveal}
          onPick={pick}
          confidenceMode={question.confidence}
          pendingChoice={myChoice === null ? pending : null}
          onConfidence={(c) => answer(pending!, c)}
        />
        {!reveal && myChoice !== null && (
          <p className="banner" style={{ marginTop: 14 }} role="status">
            Locked in. Waiting for {connected - answeredCount} more…
          </p>
        )}
        {reveal && (
          <>
            <RevealPanel reveal={reveal.reveal} correct={myResult?.correct ?? false} answered={(myResult?.choice ?? -1) >= 0}>
              <p className="muted">
                {reveal.results.filter((r) => r.correct).length} of {reveal.results.length} got it right
                {myResult?.speedBonus ? ` · you earned +${myResult.speedBonus} speed` : ''}.
              </p>
            </RevealPanel>
            <RevealFooter reveal={reveal} isHost={isHost} ready={players.find((p) => p.id === me)?.ready ?? false}
              onReady={() => sock?.send('ready')} onSkip={() => sock?.send('next')} now={now} />
          </>
        )}
      </div>
    )
  } else body = <p className="faint">Waiting for the next question…</p>

  return (
    <main className="wide">
      <div className="room-layout">
        <div>{body}</div>
        {sidebar}
      </div>
      
    </main>
  )
}

function RevealFooter({ reveal, isHost, ready, onReady, onSkip, now }: { reveal: RevealMsg; isHost: boolean; ready: boolean; onReady: () => void; onSkip: () => void; now: () => number }) {
  const [left, setLeft] = useState(0)
  useEffect(() => {
    const t = setInterval(() => setLeft(Math.max(0, Math.ceil((reveal.revealEndsAt - now()) / 1000))), 250)
    return () => clearInterval(t)
  }, [reveal, now])
  useEffect(() => {
    const on = (e: KeyboardEvent) => { if (e.key === 'Enter' && !ready) onReady() }
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [ready, onReady])
  return (
    <div className="continue-bar">
      {isHost && <button className="btn" onClick={onSkip}>Skip wait</button>}
      <button className="btn primary" onClick={onReady} disabled={ready}>
        {ready ? `Waiting for others (${left}s)` : `${reveal.isLast ? 'See results' : 'Ready for next'} (${left}s)`}
      </button>
    </div>
  )
}

function Lobby({ state, isHost, send }: { state: RoomState; isHost: boolean; send: (t: string, d?: unknown) => void }) {
  const { cats, toast } = useApp()
  const s = state.settings
  const set = (patch: Partial<RoomSettings>) => send('update_settings', { ...s, ...patch })
  const link = `${location.origin}${location.pathname}#/room/${state.code}`
  const copy = async () => {
    try { await navigator.clipboard.writeText(link); toast('Invite link copied') } catch { toast('Copy failed; share the code instead') }
  }
  const connected = state.players.filter((p) => p.connected).length
  return (
    <div className="stack">
      <section className="panel stack">
        <p className="faint">Room code</p>
        <div className="code" aria-label={`Room code ${state.code.split('').join(' ')}`}>{state.code}</div>
        <div className="row">
          <button className="btn" onClick={copy}>Copy invite link</button>
        </div>
      </section>
      <section className="panel stack" aria-label="Game settings">
        <h3>Settings {isHost ? '' : <span className="faint">(the host chooses)</span>}</h3>
        <fieldset disabled={!isHost} style={{ border: 0, padding: 0, margin: 0 }} className="stack">
          <div className="row">
            {[5, 10, 25].map((n) => (
              <button key={n} className="cat-chip" aria-pressed={s.questions === n} onClick={() => set({ questions: n })}>{n} questions</button>
            ))}
          </div>
          <div className="row">
            {[15, 25, 40].map((n) => (
              <button key={n} className="cat-chip" aria-pressed={s.timerSeconds === n} onClick={() => set({ timerSeconds: n })}>{n}s to answer</button>
            ))}
          </div>
          <div className="cat-chips">
            {cats.map((c) => (
              <button key={c.id} className="cat-chip" style={{ ['--cat' as string]: c.color }} aria-pressed={s.categories.includes(c.id)}
                onClick={() => set({ categories: s.categories.includes(c.id) ? s.categories.filter((x) => x !== c.id) : [...s.categories, c.id] })}>
                {catName(cats, c.id)}
              </button>
            ))}
          </div>
          <p className="faint">{s.categories.length ? 'Only the highlighted topics.' : 'All topics, rotated.'}</p>
          <label className="toggle"><input type="checkbox" checked={s.confidence} onChange={(e) => set({ confidence: e.target.checked })} /> Confidence mode</label>
          <label className="toggle"><input type="checkbox" checked={s.speedBonus} onChange={(e) => set({ speedBonus: e.target.checked })} /> Speed points as a tie-breaker</label>
          <label className="toggle"><input type="checkbox" checked={s.allowLateJoin} onChange={(e) => set({ allowLateJoin: e.target.checked })} /> Let people join mid-game</label>
        </fieldset>
      </section>
      {isHost ? (
        <button className="btn primary block" onClick={() => send('start_game')} disabled={connected < 1}>
          Start game with {connected} {connected === 1 ? 'player' : 'players'}
        </button>
      ) : (
        <p className="banner" role="status">Waiting for the host to start…</p>
      )}
    </div>
  )
}

function Results({ results, me, isHost, onAgain, onHome }: { results: ResultsMsg; me: string; isHost: boolean; onAgain: () => void; onHome: () => void }) {
  const { cats } = useApp()
  const byId = useMemo(() => new Map(results.leaderboard.map((p) => [p.id, p])), [results])
  const winners = (results.winners ?? []).map((id) => byId.get(id)?.name).filter(Boolean)
  const mine = results.summaries[me]
  const aw = results.awards
  const hardest = aw.hardestQuestion && results.questions.find((q) => q.id === aw.hardestQuestion!.questionId)
  return (
    <div className="stack enter-up">
      <section className="panel stack podium">
        <p className="faint">Final results</p>
        <div className="first">{winners.length ? `🏆 ${winners.join(' & ')} ${winners.length > 1 ? 'tie' : 'wins'}` : 'No winner this time'}</div>
        <ol className="players">
          {results.leaderboard.map((p, i) => (
            <li key={p.id} className={p.id === me ? 'me' : ''}>
              <Avatar p={p} />
              <span>{i + 1}. {p.name}</span>
              <span><b>{p.score}</b>{p.speed ? <span className="tag"> +{p.speed} speed</span> : null}</span>
            </li>
          ))}
        </ol>
        <div className="stat-grid">
          {aw.fastest && <div className="stat"><b>⚡ {(aw.fastest.value / 1000).toFixed(1)}s</b><span>Fastest correct: {byId.get(aw.fastest.playerId!)?.name}</span></div>}
          {aw.bestStreak && <div className="stat"><b>🔥 {aw.bestStreak.value}</b><span>Best streak: {byId.get(aw.bestStreak.playerId!)?.name}</span></div>}
          {hardest && <div className="stat"><b>{Math.round(aw.hardestQuestion!.value * 100)}% right</b><span>Hardest: {hardest.emoji} {hardest.question}</span></div>}
        </div>
      </section>
      {mine && <SummaryView s={mine} cats={cats} label="Multiplayer" />}
      <div className="continue-bar">
        <button className="btn" onClick={onHome}>Leave</button>
        {isHost ? <button className="btn primary" onClick={onAgain}>Back to lobby</button> : <span className="banner">The host can start another round.</span>}
      </div>
    </div>
  )
}
