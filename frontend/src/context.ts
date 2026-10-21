import { createContext, useContext } from 'react'
import type { Backend } from './backend'
import type { CategoryInfo, Identity } from './types'

export interface AppCtx {
  backend: Backend
  identity: Identity | null
  setIdentity: (i: Identity) => void
  cats: CategoryInfo[]
  toast: (msg: string) => void
}

export const Ctx = createContext<AppCtx>(null as unknown as AppCtx)
export const useApp = () => useContext(Ctx)
