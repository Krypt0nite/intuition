// Fully local backend for demo mode: same responses as the server, history in localStorage.
import type { AnswerResponse, CategoryInfo, DailyInfo, Identity, PlayerStats, SessionState, Tally } from '../types'
import { load, save } from '../lib/storage'
import { dailySet, hashString, mulberry32, selectQuestions, shuffleOrder, summarize, toReveal, toView,
  type History, type LocalRecord, type Pack, type PackQuestion } from '../demo/engine'
import { ApiError } from './errors'
import type { Backend, StartOptions } from './index'
import { randomName } from './online'

interface LocalSession { id: string; kind: string; day?: string; confidence: boolean; qs: PackQuestion[]; orders: number[][]; i: number; issued: number; records: LocalRecord[]; streak: number }
interface LogEntry { id: string; category: string; difficulty: string; correct: boolean; confidence: number; ms: number; at: number }

const H = 'intuition.demo.history', LOG = 'intuition.demo.log', ID = 'intuition.demo.identity', DAILY = 'intuition.demo.daily'

export class DemoBackend implements Backend {
  kind = 'demo' as const
  private sessions = new Map<string, LocalSession>()
  private byId: Map<string, PackQuestion>
  private constructor(private pack: Pack) { this.byId = new Map(pack.questions.map((q) => [q.id, q])) }

  static async create() {
    const mod = await import('../demo/pack.json')
    return new DemoBackend((mod.default ?? mod) as unknown as Pack)
  }

  identity() { return load<Identity | null>(ID, null) }
  async ensurePlayer() {
    let id = this.identity()
    if (!id) { id = { playerId: 'local', token: '', name: randomName() }; save(ID, id) }
    return id
  }
  async rename(name: string) { const id = { ...(await this.ensurePlayer()), name: name.trim().slice(0, 24) || 'Player' }; save(ID, id); return id }
  async categories(): Promise<CategoryInfo[]> {
    return this.pack.taxonomy.categories.map((c) => ({ ...c, count: this.pack.questions.filter((q) => q.category === c.id).length }))
  }

  private begin(kind: string, qs: PackQuestion[], confidence: boolean, seed: number, day?: string): SessionState {
    const rnd = mulberry32(seed)
    const s: LocalSession = { id: Math.random().toString(36).slice(2), kind, day, confidence, qs, orders: qs.map((q) => shuffleOrder(q, rnd)), i: 0, issued: Date.now(), records: [], streak: 0 }
    this.sessions.set(s.id, s)
    return this.state(s)
  }

  private state(s: LocalSession): SessionState {
    const cats = this.pack.taxonomy.categories
    const done = s.i >= s.qs.length
    return {
      sessionId: s.id, kind: s.kind, total: s.qs.length, confidence: s.confidence, answered: s.records.length,
      score: s.records.filter((r) => r.correct).length, streak: s.streak, dailyDate: s.day,
      question: done ? null : toView(s.qs[s.i], s.orders[s.i], cats, s.i, s.qs.length),
      summary: done ? summarize(s.records, this.byId) : undefined,
    }
  }

  async startSession(o: StartOptions) {
    const count = { quick: 5, standard: 10, marathon: 25 }[o.mode]
    const qs = selectQuestions(this.pack, load<History>(H, {}), { count, categories: o.categories, seed: Date.now() % 1e9, now: Date.now(), allowChains: true })
    if (!qs.length) throw new ApiError(422, 'no_questions', 'No questions match those categories')
    return this.begin(o.mode, qs, o.confidence, Date.now() % 1e9)
  }

  async getSession(id: string) {
    const s = this.sessions.get(id)
    if (!s) throw new ApiError(404, 'session_not_found', 'This game has expired; start a new one')
    return this.state(s)
  }

  async answer(sessionId: string, questionId: string, choice: number, confidence: number): Promise<AnswerResponse> {
    const s = this.sessions.get(sessionId)
    if (!s) throw new ApiError(404, 'session_not_found', 'This game has expired; start a new one')
    const q = s.qs[s.i]
    if (!q || q.id !== questionId) throw new ApiError(409, 'stale_question', "That answer doesn't match the current question")
    const order = s.orders[s.i]
    const orig = order[choice]
    const correct = orig === q.correctIndex
    const ms = Math.min(60000, Date.now() - s.issued)
    const conf = s.confidence ? confidence : 0
    s.records.push({ id: q.id, choice: orig, correct, confidence: conf, ms })
    s.streak = correct ? s.streak + 1 : 0
    const hist = load<History>(H, {})
    const h = hist[q.id] ?? { seen: 0, correct: 0, wrong: 0, last: 0, lastCorrect: false }
    hist[q.id] = { seen: h.seen + 1, correct: h.correct + (correct ? 1 : 0), wrong: h.wrong + (correct ? 0 : 1), last: Date.now(), lastCorrect: correct }
    save(H, hist)
    const log = load<LogEntry[]>(LOG, [])
    log.push({ id: q.id, category: q.category, difficulty: q.difficulty, correct, confidence: conf, ms, at: Date.now() })
    save(LOG, log.slice(-5000))
    s.i++
    s.issued = Date.now()
    const st = this.state(s)
    const score = s.records.filter((r) => r.correct).length
    if (st.summary && s.kind === 'daily' && s.day) save(DAILY, { day: s.day, score, total: s.qs.length, ms: s.records.reduce((a, r) => a + r.ms, 0) })
    return {
      result: { correct, choice, correctIndex: order.indexOf(q.correctIndex), score, answered: s.records.length, total: s.qs.length, streak: s.streak, responseMs: ms, done: !st.question },
      reveal: toReveal(q, order), next: st.question, summary: st.summary, dailyRank: st.summary && s.kind === 'daily' ? 1 : undefined,
    }
  }

  private today() { return new Date().toISOString().slice(0, 10) }

  async daily(): Promise<DailyInfo> {
    const day = this.today()
    const d = load<{ day: string; score: number; total: number; ms: number } | null>(DAILY, null)
    const mine = d && d.day === day ? d : null
    const id = await this.ensurePlayer()
    const next = new Date(day + 'T00:00:00Z').getTime() + 86400000
    const entry = mine ? { day, playerId: id.playerId, playerName: id.name, score: mine.score, total: mine.total, totalMs: mine.ms, finished: true } : undefined
    return { date: day, nextResetAt: next, played: !!mine, inProgress: false, result: entry, rank: mine ? 1 : undefined, totalPlayers: mine ? 1 : 0, leaderboard: entry ? [entry] : [] }
  }

  async startDaily(confidence: boolean) {
    const day = this.today()
    const info = await this.daily()
    if (info.played) throw new ApiError(409, 'already_played', "You've already played today's challenge", { result: info.result })
    return this.begin('daily', dailySet(this.pack, day), confidence, hashString(day), day)
  }

  async stats(): Promise<PlayerStats> {
    const log = load<LogEntry[]>(LOG, [])
    const add = (m: Record<string, Tally>, k: string, c: boolean) => { const t = (m[k] ??= { correct: 0, total: 0 }); t.total++; if (c) t.correct++ }
    const byCategory: Record<string, Tally> = {}, byDifficulty: Record<string, Tally> = {}, calibration: Record<string, Tally> = {}, concept: Record<string, Tally> = {}
    let correct = 0, best = 0, run = 0, conf = 0, surprises = 0, rt = 0
    for (const e of log) {
      add(byCategory, e.category, e.correct); add(byDifficulty, e.difficulty, e.correct)
      if (e.confidence) add(calibration, String(e.confidence), e.correct)
      this.byId.get(e.id)?.concepts.forEach((c) => add(concept, c, e.correct))
      if (e.confidence >= 2) { conf++; if (!e.correct) surprises++ }
      rt += e.ms
      if (e.correct) { correct++; run++; best = Math.max(best, run) } else run = 0
    }
    const mastered = Object.values(concept).filter((t) => t.total >= 2 && t.correct / t.total >= 0.8).length
    return {
      answered: log.length, correct, accuracy: log.length ? correct / log.length : undefined, bestStreak: best, currentStreak: run,
      byCategory, byDifficulty, calibration, conceptsMastered: mastered, conceptsSeen: Object.keys(concept).length,
      surprises, confidentAnswers: conf, surpriseRate: conf ? surprises / conf : undefined, avgResponseMs: log.length ? Math.round(rt / log.length) : undefined,
    }
  }

  wsUrl() { return null }
}
