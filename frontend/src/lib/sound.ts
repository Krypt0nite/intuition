// Tiny synthesized sound effects (no audio files). Muted state persists.
import { load, save } from './storage'

type Cue = 'select' | 'correct' | 'wrong' | 'tick' | 'reveal' | 'victory'

let ctx: AudioContext | null = null
let muted = load<boolean>('intuition.muted', false)
const listeners = new Set<(m: boolean) => void>()

export const isMuted = () => muted
export function setMuted(m: boolean) {
  muted = m
  save('intuition.muted', m)
  listeners.forEach((l) => l(m))
}
export function onMuteChange(l: (m: boolean) => void) {
  listeners.add(l)
  return () => { listeners.delete(l) }
}

function tone(freq: number, start: number, dur: number, type: OscillatorType = 'sine', gain = 0.06) {
  if (!ctx) return
  const o = ctx.createOscillator()
  const g = ctx.createGain()
  o.type = type
  o.frequency.value = freq
  g.gain.setValueAtTime(0, ctx.currentTime + start)
  g.gain.linearRampToValueAtTime(gain, ctx.currentTime + start + 0.01)
  g.gain.exponentialRampToValueAtTime(0.0001, ctx.currentTime + start + dur)
  o.connect(g).connect(ctx.destination)
  o.start(ctx.currentTime + start)
  o.stop(ctx.currentTime + start + dur + 0.05)
}

export function play(cue: Cue) {
  if (muted) return
  try {
    ctx ??= new AudioContext()
    if (ctx.state === 'suspended') void ctx.resume()
  } catch {
    return
  }
  switch (cue) {
    case 'select': tone(520, 0, 0.08, 'triangle', 0.04); break
    case 'tick': tone(880, 0, 0.05, 'square', 0.015); break
    case 'reveal': tone(392, 0, 0.18, 'sine', 0.03); tone(587, 0.08, 0.22, 'sine', 0.03); break
    case 'correct': tone(523, 0, 0.12, 'triangle'); tone(784, 0.1, 0.25, 'triangle'); break
    case 'wrong': tone(220, 0, 0.18, 'sine', 0.05); tone(196, 0.12, 0.25, 'sine', 0.04); break
    case 'victory': [523, 659, 784, 1047].forEach((f, i) => tone(f, i * 0.1, 0.3, 'triangle', 0.05)); break
  }
}
