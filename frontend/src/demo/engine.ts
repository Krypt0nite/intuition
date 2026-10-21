// Offline demo engine: a faithful, smaller port of the Go selector so the game
// is fully playable without a server. Used only in dev and demo builds.
import type { Difficulty, QuestionView, RevealView, Summary, Tally, MissedItem } from '../types'

export interface PackQuestion {
  id: string
  category: string
  subcategory: string
  difficulty: Difficulty
  questionType: string
  answerType: string
  emoji?: string
  scenario: string
  question: string
  options: string[]
  correctIndex: number
  shuffle?: boolean
  reveal: string
  explanation: string
  intuitionTrap: string
  principle: string
  funFact?: string
  caveat?: string
  assumptions?: string[]
  goDeeper?: { equation?: string; text: string; example?: string }
  concepts: string[]
  variantGroup: string
  chain?: { id: string; step: number; total: number }
  source?: string
  sourceUrl?: string
}

export interface PackCategory { id: string; name: string; emoji: string; color: string }
export interface Pack { version: string; taxonomy: { categories: PackCategory[] }; questions: PackQuestion[] }

export interface Hist { seen: number; correct: number; wrong: number; last: number; lastCorrect: boolean }
export type History = Record<string, Hist>

const LEVEL: Record<Difficulty, number> = { easy: 1, medium: 2, hard: 3, expert: 4 }
const HOUR = 3600_000
export const HARD_COOLDOWN = 72 * HOUR
const SOFT_COOLDOWN = 30 * 24 * HOUR
const MIXES = [
  [0.45, 0.4, 0.13, 0.02],
  [0.25, 0.4, 0.25, 0.1],
  [0.15, 0.35, 0.32, 0.18],
  [0.1, 0.3, 0.35, 0.25],
]

export function mulberry32(seed: number) {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

export function hashString(s: string): number {
  let h = 2166136261
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619)
  return h >>> 0
}

function tier(history: History): number {
  let n = 0, c = 0, w = 0
  for (const h of Object.values(history)) { n += h.seen; c += h.correct; w += h.wrong }
  let t = n >= 400 ? 3 : n >= 150 ? 2 : n >= 30 ? 1 : 0
  const acc = (c + 1) / (c + w + 2)
  if (n >= 20 && acc > 0.8 && t < 3) t++
  else if (n >= 20 && acc < 0.45 && t > 0) t--
  return t
}

function planLevels(n: number, mix: number[], rnd: () => number): number[] {
  const counts = mix.map((m) => Math.floor(m * n))
  const fr = mix.map((m, i) => ({ i, f: m * n - Math.floor(m * n) })).sort((a, b) => b.f - a.f)
  let total = counts.reduce((a, b) => a + b, 0)
  for (let j = 0; total < n; j++, total++) counts[fr[j % 4].i]++
  const levels: number[] = []
  counts.forEach((c, i) => { for (let k = 0; k < c; k++) levels.push(i + 1) })
  const mid = levels.slice(1, -1)
  for (let i = mid.length - 1; i > 0; i--) { const j = Math.floor(rnd() * (i + 1)); [mid[i], mid[j]] = [mid[j], mid[i]] }
  return n > 2 ? [levels[0], ...mid, levels[levels.length - 1]] : levels
}

export interface SelectOptions { count: number; categories?: string[]; seed: number; now: number; allowChains?: boolean }

export function selectQuestions(pack: Pack, history: History, opts: SelectOptions): PackQuestion[] {
  const rnd = mulberry32(opts.seed)
  const allowed = new Set(opts.categories ?? [])
  const inCat = (q: PackQuestion) => allowed.size === 0 || allowed.has(q.category)
  const singles = pack.questions.filter((q) => !q.chain && inCat(q))
  const n = opts.count
  if (!singles.length) return []
  const recentConcepts = new Map<string, number>()
  const recent = Object.entries(history).sort((a, b) => b[1].last - a[1].last).slice(0, 25)
  const byId = new Map(pack.questions.map((q) => [q.id, q]))
  for (const [id] of recent) byId.get(id)?.concepts.forEach((c) => recentConcepts.set(c, (recentConcepts.get(c) ?? 0) + 1))
  const cool = (q: PackQuestion) => { const h = history[q.id]; return !h || opts.now - h.last >= HARD_COOLDOWN }

  type Slot = { cat: string; level: number; chain?: PackQuestion }
  const slots: Slot[] = Array.from({ length: n }, () => ({ cat: '', level: 2 }))
  const used = { ids: new Set<string>(), groups: new Set<string>(), concepts: new Map<string, number>(), subs: new Set<string>() }

  if (opts.allowChains && n >= 8 && rnd() < 0.35) {
    const chains = new Map<string, PackQuestion[]>()
    pack.questions.filter((q) => q.chain && inCat(q)).forEach((q) => chains.set(q.chain!.id, [...(chains.get(q.chain!.id) ?? []), q]))
    const options = [...chains.values()].map((s) => s.sort((a, b) => a.chain!.step - b.chain!.step)).filter((s) => s.every(cool) && s.length < n)
    if (options.length) {
      const ch = options[Math.floor(rnd() * options.length)]
      const start = 1 + Math.floor(rnd() * (n - ch.length))
      ch.forEach((q, i) => { slots[start + i] = { cat: q.category, level: LEVEL[q.difficulty], chain: q }; used.groups.add(q.variantGroup) })
    }
  }
  const levels = planLevels(n, MIXES[tier(history)], rnd)
  let li = 0
  slots.forEach((s) => { if (!s.chain) s.level = levels[li++] })
  const cats = [...new Set(singles.map((q) => q.category))].sort()
  const counts = new Map<string, number>()
  slots.forEach((s, i) => {
    if (s.chain) { counts.set(s.cat, (counts.get(s.cat) ?? 0) + 1); return }
    let best = '', bestScore = -Infinity
    for (const c of cats) {
      let sc = (i + 1) / cats.length - (counts.get(c) ?? 0) + rnd() * 0.3
      if (i > 0 && slots[i - 1].cat === c) sc -= 10
      if (i > 1 && slots[i - 2].cat === c && cats.length >= 4) sc -= 2
      if (slots[i + 1]?.chain && slots[i + 1].cat === c) sc -= 10
      if (sc > bestScore) { best = c; bestScore = sc }
    }
    s.cat = best
    counts.set(best, (counts.get(best) ?? 0) + 1)
  })

  const score = (q: PackQuestion, level: number) => {
    const h = history[q.id]
    let sc = 0
    if (!h) sc += 3
    else {
      const since = opts.now - h.last
      sc += 1.5 * Math.min(1, since / SOFT_COOLDOWN) - 0.5 - 0.3 * h.seen
      if (!h.lastCorrect && since > HARD_COOLDOWN) sc += 0.5
    }
    sc -= 0.8 * Math.abs(LEVEL[q.difficulty] - level)
    for (const c of q.concepts) sc -= 1.2 * (used.concepts.get(c) ?? 0) + 0.4 * Math.min(2, recentConcepts.get(c) ?? 0)
    if (used.subs.has(q.category + '/' + q.subcategory)) sc -= 0.6
    return sc + rnd() * 0.6
  }
  const take = (q: PackQuestion) => {
    used.ids.add(q.id); used.groups.add(q.variantGroup); used.subs.add(q.category + '/' + q.subcategory)
    q.concepts.forEach((c) => used.concepts.set(c, (used.concepts.get(c) ?? 0) + 1))
  }
  const out: PackQuestion[] = []
  slots.forEach((s, i) => {
    if (s.chain) { take(s.chain); out.push(s.chain); return }
    const prev = slots[i - 1]?.cat
    const fresh = (q: PackQuestion) => !used.groups.has(q.variantGroup)
    const ladder: ((q: PackQuestion) => boolean)[] = [
      (q) => q.category === s.cat && cool(q) && fresh(q),
      (q) => q.category !== prev && cool(q) && fresh(q),
      (q) => cool(q) && fresh(q),
      fresh,
      () => true,
    ]
    for (const f of ladder) {
      const cands = singles.filter((q) => !used.ids.has(q.id) && f(q))
      if (!cands.length) continue
      const ranked = cands.map((q) => ({ q, s: score(q, s.level) })).sort((a, b) => b.s - a.s)
      const r = rnd()
      const pick = ranked[r < 0.6 || ranked.length < 2 ? 0 : r < 0.85 || ranked.length < 3 ? 1 : 2].q
      take(pick)
      out.push(pick)
      return
    }
  })
  return out
}

export function shuffleOrder(q: PackQuestion, rnd: () => number): number[] {
  const order = q.options.map((_, i) => i)
  if (q.shuffle === false) return order
  for (let i = order.length - 1; i > 0; i--) { const j = Math.floor(rnd() * (i + 1)); [order[i], order[j]] = [order[j], order[i]] }
  return order
}

export function toView(q: PackQuestion, order: number[], cats: PackCategory[], index: number, total: number): QuestionView {
  const c = cats.find((x) => x.id === q.category)
  return {
    id: q.id, category: q.category, categoryName: c?.name ?? q.category, categoryEmoji: c?.emoji ?? '', categoryColor: c?.color ?? '#888',
    subcategory: q.subcategory, difficulty: q.difficulty, questionType: q.questionType, answerType: q.answerType, emoji: q.emoji,
    scenario: q.scenario, question: q.question, index, total,
    options: order.map((o, d) => ({ index: d, label: 'ABCD'[d], text: q.options[o] })),
    chain: q.chain ? { step: q.chain.step, total: q.chain.total } : undefined,
  }
}

export function toReveal(q: PackQuestion, order: number[]): RevealView {
  return {
    questionId: q.id, correctIndex: order.indexOf(q.correctIndex), reveal: q.reveal, explanation: q.explanation,
    intuitionTrap: q.intuitionTrap, principle: q.principle, funFact: q.funFact, caveat: q.caveat, assumptions: q.assumptions,
    goDeeper: q.goDeeper, source: q.source, sourceUrl: q.sourceUrl,
  }
}

export interface LocalRecord { id: string; choice: number; correct: boolean; confidence: number; ms: number }

export function summarize(records: LocalRecord[], byId: Map<string, PackQuestion>): Summary {
  const byCategory: Record<string, Tally> = {}
  const missed: MissedItem[] = []
  const principles: string[] = []
  let score = 0, best = 0, run = 0, conf = 0, surprises = 0, rt = 0, fastest = 0
  let biggest: MissedItem | undefined
  for (const r of records) {
    const q = byId.get(r.id)!
    const t = (byCategory[q.category] ??= { correct: 0, total: 0 })
    t.total++
    rt += r.ms
    if (r.correct) {
      score++; t.correct++; run++; best = Math.max(best, run)
      if (!fastest || r.ms < fastest) fastest = r.ms
    } else {
      run = 0
      const item: MissedItem = { questionId: q.id, category: q.category, emoji: q.emoji, question: q.question, reveal: q.reveal, principle: q.principle,
        yourAnswer: r.choice >= 0 ? q.options[r.choice] : '', correctAnswer: q.options[q.correctIndex], confidence: r.confidence }
      missed.push(item)
      if (r.confidence >= 2) { surprises++; if (!biggest || r.confidence > biggest.confidence) biggest = item }
    }
    if (r.confidence >= 2) conf++
    if (!principles.includes(q.principle)) principles.push(q.principle)
  }
  const cats = Object.keys(byCategory).sort((a, b) => {
    const x = byCategory[a], y = byCategory[b]
    return y.correct / y.total - x.correct / x.total || y.total - x.total || a.localeCompare(b)
  })
  const strong = cats[0] && byCategory[cats[0]].correct > 0 ? cats[0] : undefined
  const w = cats[cats.length - 1]
  const weak = w && w !== strong && byCategory[w].correct < byCategory[w].total ? w : undefined
  const total = records.length
  return {
    score, total, accuracy: total ? score / total : 0, bestStreak: best, avgResponseMs: total ? Math.round(rt / total) : 0,
    fastestCorrectMs: fastest, byCategory, strongest: strong, weakest: weak, confidentAnswers: conf, surprises,
    surpriseRate: conf ? surprises / conf : 0, biggestSurprise: biggest, missed, principles, reliability: total ? Math.round((score / total) * 100) : 0,
  }
}

export function dailySet(pack: Pack, day: string): PackQuestion[] {
  const rnd = mulberry32(hashString('intuition:' + day))
  const singles = pack.questions.filter((q) => !q.chain).sort((a, b) => a.id.localeCompare(b.id))
  const cats = [...new Set(singles.map((q) => q.category))].sort()
  for (let i = cats.length - 1; i > 0; i--) { const j = Math.floor(rnd() * (i + 1)); [cats[i], cats[j]] = [cats[j], cats[i]] }
  const levels = [1, 2, 2, 3, 4]
  const out: PackQuestion[] = []
  levels.forEach((lvl, i) => {
    let c = singles.filter((q) => q.category === cats[i % cats.length] && LEVEL[q.difficulty] === lvl && !out.includes(q))
    if (!c.length) c = singles.filter((q) => q.category === cats[i % cats.length] && !out.includes(q))
    if (c.length) out.push(c[Math.floor(rnd() * c.length)])
  })
  return out
}
