// Reconnecting WebSocket for multiplayer. On every (re)connect it re-sends
// hello and rejoins the current room, so refreshes and network blips resume.
import type { Identity } from '../types'

export type Status = 'connecting' | 'open' | 'reconnecting' | 'closed'
export interface ServerMsg { type: string; data: any }

export class RoomSocket {
  private ws?: WebSocket
  private retry = 0
  private stopped = false
  private queue: string[] = []
  private listeners = new Set<(m: ServerMsg) => void>()
  private statusListeners = new Set<(s: Status) => void>()
  private timer?: number
  room: string | null = null
  status: Status = 'connecting'
  clockOffset = 0 // serverTime - Date.now()

  constructor(private url: string, private identity: Identity) {
    this.connect()
    window.addEventListener('online', this.kick)
  }

  private kick = () => { if (this.status !== 'open') { clearTimeout(this.timer); this.connect() } }

  private setStatus(s: Status) { this.status = s; this.statusListeners.forEach((l) => l(s)) }

  private connect() {
    if (this.stopped) return
    this.setStatus(this.retry === 0 ? 'connecting' : 'reconnecting')
    const ws = new WebSocket(this.url)
    this.ws = ws
    ws.onopen = () => {
      this.retry = 0
      this.setStatus('open')
      ws.send(JSON.stringify({ type: 'hello', data: { token: this.identity.token, name: this.identity.name } }))
      if (this.room) ws.send(JSON.stringify({ type: 'join_room', data: { code: this.room } }))
      this.queue.splice(0).forEach((m) => ws.send(m))
    }
    ws.onmessage = (ev) => {
      let msg: ServerMsg
      try { msg = JSON.parse(ev.data) } catch { return }
      if (msg.data?.serverTime) this.clockOffset = msg.data.serverTime - Date.now()
      if (msg.type === 'room_state') this.room = msg.data.code
      if (msg.type === 'room_closed' || msg.type === 'left') this.room = null
      this.listeners.forEach((l) => l(msg))
    }
    ws.onclose = () => {
      if (this.stopped) return
      const delay = Math.min(8000, 500 * 2 ** this.retry++) * (0.75 + Math.random() * 0.5)
      this.setStatus('reconnecting')
      this.timer = window.setTimeout(() => this.connect(), delay)
    }
  }

  send(type: string, data?: unknown) {
    const msg = JSON.stringify({ type, data })
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(msg)
    else this.queue.push(msg)
  }

  onMessage(l: (m: ServerMsg) => void) { this.listeners.add(l); return () => { this.listeners.delete(l) } }
  onStatus(l: (s: Status) => void) { this.statusListeners.add(l); return () => { this.statusListeners.delete(l) } }
  serverNow() { return Date.now() + this.clockOffset }

  close() {
    this.stopped = true
    window.removeEventListener('online', this.kick)
    clearTimeout(this.timer)
    this.ws?.close()
    this.setStatus('closed')
  }
}
