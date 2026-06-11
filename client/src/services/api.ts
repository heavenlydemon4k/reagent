import axios, {
  type AxiosInstance,
  type AxiosRequestConfig,
  type AxiosError,
} from 'axios'

const API_BASE_URL = import.meta.env.VITE_API_URL ?? 'http://localhost:8000'

let isRefreshing = false
let refreshQueue: Array<{
  resolve: (token: string) => void
  reject: (err: Error) => void
}> = []

function processQueue(error: Error | null, token?: string): void {
  for (const promise of refreshQueue) {
    if (error || !token) {
      promise.reject(error ?? new Error('Token refresh failed'))
    } else {
      promise.resolve(token)
    }
  }
  refreshQueue = []
}

export const api: AxiosInstance = axios.create({
  baseURL: API_BASE_URL,
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json',
    Accept: 'application/json',
  },
})

api.interceptors.request.use(
  async (config) => {
    const token = localStorage.getItem('reagent_token')
    if (token) {
      config.headers = config.headers ?? {}
      config.headers.Authorization = `Bearer ${token}`
    }
    return config
  },
  (error) => Promise.reject(error)
)

api.interceptors.response.use(
  (response) => response,
  async (error: AxiosError) => {
    const originalRequest = error.config as AxiosRequestConfig & {
      _retry?: boolean
    }

    if (!originalRequest) {
      return Promise.reject(error)
    }

    if (error.response?.status === 401 && !originalRequest._retry) {
      if (isRefreshing) {
        return new Promise((resolve, reject) => {
          refreshQueue.push({ resolve, reject })
        })
          .then((token) => {
            originalRequest.headers = originalRequest.headers ?? {}
            originalRequest.headers.Authorization = `Bearer ${token}`
            return api(originalRequest)
          })
          .catch((err) => Promise.reject(err))
      }

      originalRequest._retry = true
      isRefreshing = true

      try {
        const refreshToken = localStorage.getItem('reagent_refresh_token')
        if (!refreshToken) {
          throw new Error('No refresh token available')
        }

        const response = await axios.post<{
          access_token: string
          refresh_token: string
          expires_at: number
        }>(`${API_BASE_URL}/auth/refresh`, {
          refresh_token: refreshToken,
        })

        const newTokens = response.data
        localStorage.setItem('reagent_token', newTokens.access_token)
        localStorage.setItem('reagent_refresh_token', newTokens.refresh_token)

        processQueue(null, newTokens.access_token)

        originalRequest.headers = originalRequest.headers ?? {}
        originalRequest.headers.Authorization = `Bearer ${newTokens.access_token}`
        return api(originalRequest)
      } catch (refreshError) {
        processQueue(
          refreshError instanceof Error
            ? refreshError
            : new Error('Token refresh failed')
        )
        localStorage.removeItem('reagent_token')
        localStorage.removeItem('reagent_refresh_token')
        window.location.href = '/login'
        return Promise.reject(refreshError)
      } finally {
        isRefreshing = false
      }
    }

    return Promise.reject(error)
  }
)

export async function checkServerHealth(): Promise<boolean> {
  try {
    await api.get('/health', { timeout: 5000 })
    return true
  } catch {
    return false
  }
}

export async function createChatSession(
  title?: string,
  context?: object
): Promise<{ id: string; title: string; created_at: number }> {
  const response = await api.post('/chat/sessions', { title, context })
  return response.data
}

export async function listChatSessions(): Promise<Array<{
  id: string
  title: string
  created_at: number
  updated_at: number
  message_count: number
}>> {
  const response = await api.get('/chat/sessions')
  return response.data
}

export async function getChatSession(
  sessionId: string
): Promise<{ session_id: string; messages: Array<unknown> }> {
  const response = await api.get(`/chat/sessions/${sessionId}`)
  return response.data
}

export async function sendChatSessionMessage(
  sessionId: string,
  content: string
): Promise<{
  session_id: string
  message: unknown
  cost_usd: number
  model: string
}> {
  const response = await api.post(`/chat/sessions/${sessionId}/messages`, { content })
  return response.data
}

export async function sendChatCard(
  sessionId: string,
  cardData: object
): Promise<unknown> {
  const response = await api.post(`/chat/sessions/${sessionId}/cards`, cardData)
  return response.data
}

export async function fetchEmailSource(emailId: string): Promise<{
  id: string
  subject: string
  from: string
  to: string[]
  body_text: string
  received_at: string
  labels: string[]
}> {
  const response = await api.get(`/chat/emails/${emailId}/source`)
  return response.data
}

export async function getProfile(): Promise<unknown> {
  const response = await api.get('/profile/me')
  return response.data
}

export async function updateProfile(updates: object): Promise<unknown> {
  const response = await api.put('/profile/me', updates)
  return response.data
}

export async function getPreferences(): Promise<{
  agent_tone: string
  agent_detail_level: string
  auto_handle_confidence: number
  voice_enabled: boolean
  notifications_enabled: boolean
}> {
  const response = await api.get('/profile/me/preferences')
  return response.data
}

import type {
  EmailAccount,
  CalendarEvent,
  FreeBusyResponse,
  CalendarEventCreate,
} from '../types/cards'
import type { ContactProfile, ThreadSummary } from '../types/contact'

// Account management
export async function getConnectedAccounts(): Promise<EmailAccount[]> {
  const r = await api.get<EmailAccount[]>('/accounts')
  return r.data
}
export async function initiateOAuth(provider: string): Promise<string> {
  const r = await api.post<{ url: string }>('/accounts/oauth/init', { provider })
  return r.data.url
}
export async function disconnectAccount(accountId: string): Promise<void> {
  await api.delete(`/accounts/${accountId}`)
}
export async function setServerActiveAccount(accountId: string | null): Promise<void> {
  await api.put('/accounts/active', { account_id: accountId })
}

// Card batch
export async function fetchBatch(count: number): Promise<{ cards: import('../types/cards').DecisionCard[]; status: string }> {
  const r = await api.get<{ cards: import('../types/cards').DecisionCard[]; status: string }>('/cards/batch', { params: { count } })
  return r.data
}

// Calendar
export async function getCalendarEvents(days?: number): Promise<CalendarEvent[]> {
  const r = await api.get<CalendarEvent[]>('/calendar/events', { params: { days } })
  return r.data
}
export async function checkFreeBusy(date: string): Promise<FreeBusyResponse> {
  const r = await api.post<FreeBusyResponse>('/calendar/free-busy', { date })
  return r.data
}
export async function createCalendarEvent(event: CalendarEventCreate): Promise<CalendarEvent> {
  const r = await api.post<CalendarEvent>('/calendar/events', event)
  return r.data
}
export async function sendDraft(draftId: string): Promise<{ status: string }> {
  const r = await api.post<{ status: string }>(`/drafts/${draftId}/send`)
  return r.data
}

// Contacts
export async function getContactProfile(contactId: string): Promise<ContactProfile> {
  const r = await api.get<ContactProfile>(`/contacts/${contactId}`)
  return r.data
}
export async function getContactTimeline(contactId: string, limit?: number): Promise<ThreadSummary[]> {
  const r = await api.get<ThreadSummary[]>(`/contacts/${contactId}/timeline`, { params: { limit } })
  return r.data
}

// Decisions / drafting
export async function submitDecision(payload: { card_id: string; decision: string; input?: string }): Promise<{ draft_id: string; draft_body: string; subject_line: string }> {
  const r = await api.post<{ draft_id: string; draft_body: string; subject_line: string }>('/decisions', payload)
  return r.data
}

// Onboarding
export async function completeOnboarding(): Promise<void> {
  await api.post('/onboarding/complete')
}
