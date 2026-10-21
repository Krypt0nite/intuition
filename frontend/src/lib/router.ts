import { useEffect, useState } from 'react'

// Hash routing keeps the build static-host friendly: #/, #/play/quick, #/room/X7K9P …
export function useRoute(): string[] {
  const parse = () => window.location.hash.replace(/^#\/?/, '').split('/').filter(Boolean)
  const [parts, setParts] = useState(parse)
  useEffect(() => {
    const on = () => setParts(parse())
    window.addEventListener('hashchange', on)
    return () => window.removeEventListener('hashchange', on)
  }, [])
  return parts
}

export function go(path: string) {
  window.location.hash = path.startsWith('/') ? path : '/' + path
}
