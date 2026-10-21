import type { AnswerResponse, CategoryInfo, DailyInfo, Identity, PlayerStats, SessionState } from '../types'
import { load, remove, save } from '../lib/storage'
import { ApiError } from './errors'
import type { Backend, StartOptions } from './index'

const KEY = 'intuition.identity'
const ADJ = ['Curious', 'Quantum', 'Orbiting', 'Buoyant', 'Entangled', 'Refracted', 'Spinning', 'Catalytic', 'Tidal', 'Gravitational']
const NOUN = ['Quokka', 'Neutrino', 'Axolotl', 'Comet', 'Otter', 'Photon', 'Pangolin', 'Nebula', 'Tardigrade', 'Electron']
export const randomName = () => `${ADJ[Math.floor(Math.random() * ADJ.length)]} ${NOUN[Math.floor(Math.random() * NOUN.length)]}`

export class OnlineBackend implements Backend {
  kind = 'online' as const
  private id: Identity | null = load<Identity | null>(KEY, null)

  identity() { return this.id }

  private async req<T>(method: string, path: string, body?: unknown, auth = true, retry = true): Promise<T> {
    if (auth) await this.ensurePlayer()
    const res = await fetch(path, {
      method,
      headers: { 'Content-Type': 'application/json', ...(auth && this.id ? { Authorization: `Bearer ${this.id.token}` } : {}) },
      body: body === undefined ? undefined : JSON.stringify(body),
    }).catch(() => { throw new ApiError(0, 'network', 'Connection lost. Check your network and try again.') })
    const json = await res.json().catch(() => ({}))
    if (res.status === 401 && auth && retry) {
      // Server secret changed or identity is stale: register again once.
      remove(KEY)
      this.id = null
      return this.req<T>(method, path, body, auth, false)
    }
    if (!res.ok) throw new ApiError(res.status, json.error ?? 'error', json.message ?? `Request failed (${res.status})`, json)
    return json as T
  }

  async ensurePlayer(): Promise<Identity> {
    if (this.id) return this.id
    const r = await this.req<{ playerId: string; token: string; name: string }>('POST', '/api/players', { name: randomName() }, false)
    this.id = { playerId: r.playerId, token: r.token, name: r.name }
    save(KEY, this.id)
    return this.id
  }

  async rename(name: string) {
    const r = await this.req<{ name: string }>('PATCH', '/api/players/me', { name })
    this.id = { ...this.id!, name: r.name }
    save(KEY, this.id)
    return this.id
  }

  async categories() {
    return (await this.req<{ categories: CategoryInfo[] }>('GET', '/api/categories', undefined, false)).categories
  }
  startSession(o: StartOptions) { return this.req<SessionState>('POST', '/api/sessions', o) }
  getSession(id: string) { return this.req<SessionState>('GET', `/api/sessions/${id}`) }
  answer(sessionId: string, questionId: string, choice: number, confidence: number) {
    return this.req<AnswerResponse>('POST', `/api/sessions/${sessionId}/answers`, { questionId, choice, confidence })
  }
  daily() { return this.req<DailyInfo>('GET', '/api/daily') }
  startDaily(confidence: boolean) { return this.req<SessionState>('POST', '/api/daily/start', { confidence }) }
  stats() { return this.req<PlayerStats>('GET', '/api/players/me/stats') }
  wsUrl() {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    return `${proto}//${location.host}/ws`
  }
}
