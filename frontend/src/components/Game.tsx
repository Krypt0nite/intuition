import { useEffect, useRef, useState } from 'react'
import type { QuestionView, RevealView } from '../types'

const DIFF: Record<string, string> = { easy: 'Easy', medium: 'Medium', hard: 'Hard', expert: 'Expert' }
export const CONFIDENCE = [
  { value: 1, emoji: '🤔', label: 'Guessing' },
  { value: 2, emoji: '🙂', label: 'Fairly sure' },
  { value: 3, emoji: '😎', label: 'Absolutely certain' },
]

export function Progress({ index, total, results, streak }: { index: number; total: number; results: (boolean | null)[]; streak: number }) {
  return (
    <div className="progress" aria-label={`Question ${index + 1} of ${total}`}>
      <span>
        {index + 1}/{total}
      </span>
      <div className="dots" aria-hidden="true">
        {Array.from({ length: total }, (_, i) => (
          <span key={i} className={'dot ' + (results[i] === true ? 'ok' : results[i] === false ? 'no' : i === index ? 'now' : '')} />
        ))}
      </div>
      {streak >= 3 && <span className="streak">🔥 {streak} in a row</span>}
    </div>
  )
}

export function TimerBar({ deadline, duration, now }: { deadline: number; duration: number; now: () => number }) {
  const [left, setLeft] = useState(deadline - now())
  useEffect(() => {
    let raf = 0
    const tick = () => {
      setLeft(Math.max(0, deadline - now()))
      raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [deadline, now])
  const frac = Math.max(0, Math.min(1, left / duration))
  const secs = Math.ceil(left / 1000)
  return (
    <div>
      <div className="row" style={{ justifyContent: 'space-between' }}>
        <span className="faint">Time left</span>
        <span className="faint" aria-live={secs <= 5 ? 'polite' : 'off'}>{secs}s</span>
      </div>
      <div className={'timer' + (secs <= 5 ? ' low' : '')} role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(frac * 100)} aria-label="Time remaining">
        <i style={{ transform: `scaleX(${frac})` }} />
      </div>
    </div>
  )
}

export interface OptionState {
  picked: number | null
  reveal?: RevealView | null
  measuring?: boolean
  distribution?: number[]
}

export function QuestionCard({
  q, state, locked, onPick, confidenceMode, onConfidence, pendingChoice,
}: {
  q: QuestionView
  state: OptionState
  locked: boolean
  onPick: (i: number) => void
  confidenceMode?: boolean
  onConfidence?: (c: number) => void
  pendingChoice?: number | null
}) {
  const headingRef = useRef<HTMLHeadingElement>(null)
  useEffect(() => headingRef.current?.focus(), [q.id])
  const r = state.reveal
  const total = state.distribution?.reduce((a, b) => a + b, 0) ?? 0
  const selected = state.picked ?? pendingChoice ?? null
  return (
    <article className={'panel qcard' + (state.measuring ? ' measuring' : '')} style={{ ['--cat' as string]: q.categoryColor }}>
      <div className="tagline">
        <span className="cat-dot" aria-hidden="true" />
        <span>
          {q.categoryEmoji} {q.categoryName}
        </span>
        <span className="diff">{DIFF[q.difficulty]}</span>
        {q.chain && <span className="chain">🔗 Chain round, step {q.chain.step} of {q.chain.total}</span>}
      </div>
      {q.emoji && <span className="emoji" aria-hidden="true">{q.emoji}</span>}
      <p className="scenario">{q.scenario}</p>
      <h2 className="question" tabIndex={-1} ref={headingRef}>
        {q.question}
      </h2>
      <div className="options" role="group" aria-label="Answers">
        {q.options.map((o) => {
          const isCorrect = r && o.index === r.correctIndex
          const isPicked = selected === o.index
          let cls = 'opt'
          if (r) cls += isCorrect ? ' correct flare' : isPicked ? ' wrong' : ' dim'
          else if (isPicked) cls += ' picked'
          else if (selected !== null) cls += ' dim'
          const share = total ? (state.distribution![o.index] ?? 0) / total : 0
          return (
            <button
              key={o.index}
              className={cls}
              disabled={locked || !!r}
              onClick={() => onPick(o.index)}
              aria-pressed={isPicked}
              aria-label={`Option ${o.label}: ${o.text}${r ? (isCorrect ? ', correct answer' : isPicked ? ', your answer, incorrect' : '') : isPicked ? ', selected' : ''}`}
            >
              <span className="key" aria-hidden="true">{o.label}</span>
              <span>{o.text}</span>
              <span className="mark" aria-hidden="true">
                {r && isCorrect && '✓ Correct'}
                {r && !isCorrect && isPicked && '✕ Your pick'}
                {!r && isPicked && 'Locked in'}
                {r && total > 0 && <span className="dist"> {state.distribution![o.index] ?? 0}</span>}
              </span>
              {r && total > 0 && <span className="bar" style={{ width: `${share * 100}%` }} aria-hidden="true" />}
            </button>
          )
        })}
      </div>
      {confidenceMode && pendingChoice != null && !r && onConfidence && (
        <div className="confidence enter-up">
          <p className="muted">How sure are you about {q.options[pendingChoice].label}?</p>
          <div className="choices">
            {CONFIDENCE.map((c) => (
              <button key={c.value} onClick={() => onConfidence(c.value)}>
                <span aria-hidden="true">{c.emoji}</span> {c.label}
              </button>
            ))}
          </div>
        </div>
      )}
    </article>
  )
}

export function RevealPanel({ reveal, correct, answered, children }: { reveal: RevealView; correct: boolean | null; answered: boolean; children?: React.ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => ref.current?.focus(), [reveal.questionId])
  return (
    <div className="enter-up" aria-live="polite">
      <div className="verdict" tabIndex={-1} ref={ref}>
        <div className={'line ' + (correct ? 'ok' : 'no')}>
          {correct ? 'You were right.' : answered ? 'Not quite.' : "Time's up."}
        </div>
        <div className="headline">{reveal.reveal}</div>
      </div>
      {children}
      <div className="explain">
        <section>
          <h4>Why?</h4>
          <p>{reveal.explanation}</p>
        </section>
        <section>
          <h4>🧠 The trick</h4>
          <p>{reveal.intuitionTrap}</p>
        </section>
        <section>
          <h4>⚡ The principle</h4>
          <p className="principle">{reveal.principle}</p>
        </section>
        {reveal.funFact && (
          <section>
            <h4>😮 Wait, really?</h4>
            <p className="funfact">{reveal.funFact}</p>
          </section>
        )}
        {(reveal.caveat || reveal.assumptions?.length) && (
          <section className="caveat">
            {reveal.assumptions?.length ? <p>Assuming: {reveal.assumptions.join('; ')}.</p> : null}
            {reveal.caveat && <p>⚠️ {reveal.caveat}</p>}
          </section>
        )}
        {(reveal.goDeeper || reveal.source) && (
          <details className="deeper">
            <summary>🔍 Go deeper</summary>
            {reveal.goDeeper?.equation && <div className="equation">{reveal.goDeeper.equation}</div>}
            {reveal.goDeeper && <p>{reveal.goDeeper.text}</p>}
            {reveal.goDeeper?.example && <p className="muted">{reveal.goDeeper.example}</p>}
            {reveal.source && (
              <p className="faint" style={{ marginTop: 10 }}>
                Source: {reveal.sourceUrl ? <a href={reveal.sourceUrl} target="_blank" rel="noreferrer">{reveal.source}</a> : reveal.source}
              </p>
            )}
          </details>
        )}
      </div>
    </div>
  )
}

// Keyboard: 1–4 or A–D answer; Enter/Space continue.
export function useAnswerKeys(enabled: boolean, count: number, onPick: (i: number) => void) {
  useEffect(() => {
    if (!enabled) return
    const on = (e: KeyboardEvent) => {
      if (e.target instanceof HTMLInputElement || e.metaKey || e.ctrlKey || e.altKey) return
      const k = e.key.toLowerCase()
      const i = '1234'.indexOf(k) >= 0 ? '1234'.indexOf(k) : 'abcd'.indexOf(k)
      if (i >= 0 && i < count) {
        e.preventDefault()
        onPick(i)
      }
    }
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [enabled, count, onPick])
}

export function useEnterKey(enabled: boolean, fn: () => void) {
  useEffect(() => {
    if (!enabled) return
    const on = (e: KeyboardEvent) => {
      if (e.key === 'Enter' && !(e.target instanceof HTMLButtonElement) && !(e.target instanceof HTMLInputElement)) {
        e.preventDefault()
        fn()
      }
    }
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [enabled, fn])
}
