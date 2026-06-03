import { ref, readonly } from 'vue'

export interface ToolResult {
  tool: string
  success: boolean
  data: any
  error?: string
}

export interface SreAgentState {
  statusMessage: string
  toolResults: ToolResult[]
  response: string
  error: string
  phase: string // 'idle' | 'executing' | 'analyzing' | 'done' | 'error'
}

export function useSreAgent() {
  const state = ref<SreAgentState>({
    statusMessage: '',
    toolResults: [],
    response: '',
    error: '',
    phase: 'idle',
  })

  const streaming = ref(false)
  const abortController = ref<AbortController | null>(null)

  function reset() {
    state.value = {
      statusMessage: '',
      toolResults: [],
      response: '',
      error: '',
      phase: 'idle',
    }
  }

  async function send(message: string): Promise<void> {
    if (!message.trim() || streaming.value) return

    state.value = {
      statusMessage: '',
      toolResults: [],
      response: '',
      error: '',
      phase: 'executing',
    }
    streaming.value = true

    const controller = new AbortController()
    abortController.value = controller

    try {
      const token = localStorage.getItem('token')
      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
      }
      if (token) {
        headers['Authorization'] = `Bearer ${token}`
      }

      const response = await fetch('/api/ai/sre/agent', {
        method: 'POST',
        headers,
        body: JSON.stringify({ message }),
        signal: controller.signal,
      })

      if (!response.ok) {
        const text = await response.text()
        try {
          const err = JSON.parse(text)
          throw new Error(err.error || `HTTP ${response.status}`)
        } catch (e: any) {
          if (e.message && e.message !== `HTTP ${response.status}`) throw e
          throw new Error(`Server error (${response.status})`)
        }
      }

      const reader = response.body?.getReader()
      if (!reader) throw new Error('No response body')

      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const parts = buffer.split('\n\n')
        buffer = parts.pop() || ''

        for (const part of parts) {
          const lines = part.split('\n')
          let eventType = ''
          let eventData = ''

          for (const line of lines) {
            if (line.startsWith('event: ')) {
              eventType = line.slice(7).trim()
            } else if (line.startsWith('data: ')) {
              eventData = line.slice(6).trim()
            }
          }

          handleEvent(eventType, eventData)
        }
      }
    } catch (e: any) {
      if (e.name === 'AbortError') {
        state.value.phase = 'done'
        state.value.statusMessage = '已取消'
      } else {
        state.value.phase = 'error'
        state.value.error = e.message || 'SRE Agent error'
      }
    } finally {
      streaming.value = false
      abortController.value = null
    }
  }

  function abort() {
    if (abortController.value) {
      abortController.value.abort()
    }
  }

  function handleEvent(eventType: string, data: string) {
    switch (eventType) {
      case 'status':
        state.value.statusMessage = data
        if (data.includes('AI 正在分析')) {
          state.value.phase = 'analyzing'
        }
        break

      case 'tool_result':
        try {
          const tr = JSON.parse(data)
          state.value.toolResults.push(tr)
        } catch { /* ignore */ }
        break

      case 'token':
        state.value.response += data
        break

      case 'error':
        try {
          const err = JSON.parse(data)
          state.value.error = err.message || data
        } catch {
          state.value.error = data
        }
        state.value.phase = 'error'
        break

      case 'done':
        state.value.phase = 'done'
        break
    }
  }

  return {
    state: readonly(state),
    streaming: readonly(streaming),
    send,
    abort,
    reset,
  }
}
