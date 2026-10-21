export type Difficulty = 'easy' | 'medium' | 'hard' | 'expert'
export type Mode = 'quick' | 'standard' | 'marathon'

export interface CategoryInfo {
  id: string
  name: string
  emoji: string
  color: string
  count?: number
}

export interface OptionView { index: number; label: string; text: string }

export interface QuestionView {
  id: string
  category: string
  categoryName: string
  categoryEmoji: string
  categoryColor: string
  subcategory: string
  difficulty: Difficulty
  questionType: string
  answerType: string
  emoji?: string
  scenario: string
  question: string
  options: OptionView[]
  index: number
  total: number
  chain?: { step: number; total: number }
}

export interface GoDeeper { equation?: string; text: string; example?: string }

export interface RevealView {
  questionId: string
  correctIndex: number
  reveal: string
  explanation: string
  intuitionTrap: string
  principle: string
  funFact?: string
  caveat?: string
  assumptions?: string[]
  goDeeper?: GoDeeper
  source?: string
  sourceUrl?: string
}

export interface Tally { correct: number; total: number }

export interface MissedItem {
  questionId: string
  category: string
  emoji?: string
  question: string
  reveal: string
  principle: string
  yourAnswer: string
  correctAnswer: string
  confidence: number
}

export interface Summary {
  score: number
  total: number
  accuracy: number
  bestStreak: number
  avgResponseMs: number
  fastestCorrectMs: number
  byCategory: Record<string, Tally>
  strongest?: string
  weakest?: string
  confidentAnswers: number
  surprises: number
  surpriseRate: number
  biggestSurprise?: MissedItem
  missed: MissedItem[]
  principles: string[]
  reliability: number
}

export interface AnswerResult {
  correct: boolean
  choice: number
  correctIndex: number
  score: number
  answered: number
  total: number
  streak: number
  responseMs: number
  duplicate?: boolean
  done: boolean
}

export interface SessionState {
  sessionId: string
  kind: string
  total: number
  confidence: boolean
  answered: number
  score: number
  streak: number
  question: QuestionView | null
  dailyDate?: string
  summary?: Summary
}

export interface AnswerResponse {
  result: AnswerResult
  reveal: RevealView
  next: QuestionView | null
  summary?: Summary
  dailyRank?: number
}

export interface DailyEntry {
  day: string
  playerId: string
  playerName?: string
  score: number
  total: number
  totalMs: number
  finished: boolean
}

export interface DailyInfo {
  date: string
  nextResetAt: number
  played: boolean
  inProgress: boolean
  result?: DailyEntry
  rank?: number
  totalPlayers: number
  leaderboard: DailyEntry[]
}

export interface PlayerStats {
  answered: number
  correct: number
  accuracy?: number
  bestStreak: number
  currentStreak: number
  byCategory: Record<string, Tally>
  byDifficulty: Record<string, Tally>
  calibration: Record<string, Tally>
  conceptsMastered: number
  conceptsSeen: number
  surprises: number
  confidentAnswers: number
  surpriseRate?: number
  avgResponseMs?: number
  recentSessions?: { id: string; kind: string; score: number; total: number; finishedAt: string }[] | null
}

export interface Identity { playerId: string; token: string; name: string }

// ---- Multiplayer ----
export interface RoomSettings {
  questions: number
  timerSeconds: number
  revealSeconds: number
  categories: string[]
  confidence: boolean
  speedBonus: boolean
  allowLateJoin: boolean
}

export interface RoomPlayer {
  id: string
  name: string
  connected: boolean
  isHost: boolean
  score: number
  speed: number
  streak: number
  answered: boolean
  ready: boolean
  late: boolean
}

export type RoomPhase = 'lobby' | 'question' | 'reveal' | 'results' | 'closed'

export interface RoomState {
  code: string
  phase: RoomPhase
  hostId: string
  settings: RoomSettings
  players: RoomPlayer[]
  questionIndex: number
  total: number
  deadline?: number
  revealEndsAt?: number
  serverTime: number
}

export interface QuestionMsg { question: QuestionView; deadline: number; durationMs: number; serverTime: number; confidence: boolean }

export interface PlayerResult { playerId: string; choice: number; correct: boolean; points: number; speedBonus: number; responseMs: number; confidence: number }

export interface RevealMsg {
  index: number
  total: number
  reveal: RevealView
  results: PlayerResult[]
  distribution: number[]
  leaderboard: RoomPlayer[]
  revealEndsAt: number
  serverTime: number
  isLast: boolean
}

export interface Award { playerId?: string; questionId?: string; value: number; label?: string }

export interface ResultsMsg {
  leaderboard: RoomPlayer[]
  summaries: Record<string, Summary>
  awards: { fastest?: Award; bestStreak?: Award; hardestQuestion?: Award }
  winners: string[] | null
  questions: { id: string; emoji?: string; question: string; reveal: string; category: string; accuracy?: number }[]
}
