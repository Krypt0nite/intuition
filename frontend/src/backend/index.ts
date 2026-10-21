// The UI talks to one Backend interface. OnlineBackend uses the Go server;
// DemoBackend runs everything locally when the server isn't reachable.
import type { AnswerResponse, CategoryInfo, DailyInfo, Identity, Mode, PlayerStats, SessionState } from '../types'
import { OnlineBackend } from './online'

export interface StartOptions { mode: Mode; categories: string[]; confidence: boolean }

export interface Backend {
  kind: 'online' | 'demo'
  identity(): Identity | null
  ensurePlayer(): Promise<Identity>
  rename(name: string): Promise<Identity>
  categories(): Promise<CategoryInfo[]>
  startSession(o: StartOptions): Promise<SessionState>
  getSession(id: string): Promise<SessionState>
  answer(sessionId: string, questionId: string, choice: number, confidence: number): Promise<AnswerResponse>
  daily(): Promise<DailyInfo>
  startDaily(confidence: boolean): Promise<SessionState>
  stats(): Promise<PlayerStats>
  wsUrl(): string | null
}

export { ApiError } from './errors'
import { ApiError } from './errors'

async function serverUp(): Promise<boolean> {
  try {
    const ctl = new AbortController()
    const t = setTimeout(() => ctl.abort(), 2500)
    const r = await fetch('/api/health', { signal: ctl.signal })
    clearTimeout(t)
    if (!r.ok) return false
    const j = await r.json()
    return j?.ok === true
  } catch {
    return false
  }
}

export async function detectBackend(): Promise<Backend> {
  if (!__FORCE_DEMO__ && (await serverUp())) return new OnlineBackend()
  if (__DEMO_FALLBACK__) {
    const { DemoBackend } = await import('./demo')
    return DemoBackend.create()
  }
  throw new ApiError(503, 'offline', 'The game server is unreachable. Check your connection and try again.')
}
