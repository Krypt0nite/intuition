import { describe, expect, it } from 'vitest'
import pack from './pack.json'
import { HARD_COOLDOWN, dailySet, selectQuestions, summarize, type History, type Pack } from './engine'

const P = pack as unknown as Pack

describe('demo selection engine', () => {
  it('never repeats a question or variant group inside a session', () => {
    for (let seed = 0; seed < 100; seed++) {
      const qs = selectQuestions(P, {}, { count: 25, seed, now: Date.now(), allowChains: true })
      expect(qs).toHaveLength(25)
      expect(new Set(qs.map((q) => q.id)).size).toBe(25)
      expect(new Set(qs.map((q) => q.variantGroup)).size).toBe(25)
    }
  })

  it('rotates categories', () => {
    for (let seed = 0; seed < 100; seed++) {
      const qs = selectQuestions(P, {}, { count: 10, seed, now: Date.now() })
      expect(new Set(qs.map((q) => q.category)).size).toBeGreaterThanOrEqual(7)
      qs.forEach((q, i) => i > 0 && expect(q.category).not.toBe(qs[i - 1].category))
    }
  })

  it('respects the hard cooldown while unseen questions remain', () => {
    const now = Date.now()
    const history: History = {}
    let s = 0
    const played = new Set<string>()
    for (let game = 0; game < 8; game++) {
      const qs = selectQuestions(P, history, { count: 10, seed: s++, now })
      for (const q of qs) {
        expect(played.has(q.id)).toBe(false)
        played.add(q.id)
        history[q.id] = { seen: 1, correct: 1, wrong: 0, last: now - 1000, lastCorrect: true }
      }
    }
    expect(now - (now - 1000)).toBeLessThan(HARD_COOLDOWN)
  })

  it('builds the same daily set for everyone', () => {
    const a = dailySet(P, '2026-10-06').map((q) => q.id)
    expect(a).toEqual(dailySet(P, '2026-10-06').map((q) => q.id))
    expect(a).not.toEqual(dailySet(P, '2026-10-07').map((q) => q.id))
    expect(a).toHaveLength(5)
  })

  it('summarizes surprises', () => {
    const byId = new Map(P.questions.map((q) => [q.id, q]))
    const s = summarize([
      { id: 'phy-elevator-scale', choice: 0, correct: true, confidence: 1, ms: 2000 },
      { id: 'chem-rusted-nail-mass', choice: 0, correct: false, confidence: 3, ms: 4000 },
    ], byId)
    expect(s.score).toBe(1)
    expect(s.surprises).toBe(1)
    expect(s.biggestSurprise?.questionId).toBe('chem-rusted-nail-mass')
    expect(s.reliability).toBe(50)
  })
})
