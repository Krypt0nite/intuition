import type { Summary, CategoryInfo } from '../types'

export function shareText(s: Summary, cats: CategoryInfo[], label = ''): string {
  const lines = [`🔬 INTUITION${label ? ' · ' + label : ''}`, '', `I scored ${s.score}/${s.total}`, '']
  for (const c of cats) {
    const t = s.byCategory[c.id]
    if (t) lines.push(`${c.emoji} ${c.name}: ${t.correct}/${t.total}`)
  }
  lines.push('', 'My intuition is apparently…', `${s.reliability}% reliable`)
  if (s.surprises > 0) lines.push(`(and confidently wrong ${s.surprises}×)`)
  lines.push('', 'Can you beat me?')
  return lines.join('\n')
}

export async function shareOrCopy(text: string): Promise<'shared' | 'copied' | 'failed'> {
  try {
    if (navigator.share) {
      await navigator.share({ text })
      return 'shared'
    }
  } catch {
    /* user cancelled or unsupported — fall through to copy */
  }
  try {
    await navigator.clipboard.writeText(text)
    return 'copied'
  } catch {
    return 'failed'
  }
}

export function useReducedMotion(): boolean {
  try {
    return window.matchMedia('(prefers-reduced-motion: reduce)').matches
  } catch {
    return false
  }
}
