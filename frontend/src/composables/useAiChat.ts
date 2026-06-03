import { ref, readonly } from 'vue'

export interface ChatMessage {
  role: 'user' | 'assistant'
  content: string
}

export interface AiChatOptions {
  agentType: string
  problemId?: number
  submissionId?: number
}

const defaultOptions: AiChatOptions = {
  agentType: 'coach',
  problemId: 0,
}

export function useAiChat(options: Partial<AiChatOptions> = {}) {
  const messages = ref<ChatMessage[]>([])
  const streaming = ref(false)
  const currentToken = ref('')
  const error = ref('')
  const abortController = ref<AbortController | null>(null)

  const mergedOptions = { ...defaultOptions, ...options }

  function addMessage(role: 'user' | 'assistant', content: string) {
    messages.value.push({ role, content })
  }

  function updateOptions(opts: Partial<AiChatOptions>) {
    Object.assign(mergedOptions, opts)
  }

  async function send(userMessage: string): Promise<void> {
    if (!userMessage.trim() || streaming.value) return

    addMessage('user', userMessage)
    streaming.value = true
    currentToken.value = ''
    error.value = ''

    const assistantMsg: ChatMessage = { role: 'assistant', content: '' }
    messages.value.push(assistantMsg)

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

      const history = messages.value
        .slice(0, -2)
        .filter((m) => m.content.length > 0)

      const body = JSON.stringify({
        agent_type: mergedOptions.agentType,
        problem_id: mergedOptions.problemId,
        submission_id: mergedOptions.submissionId || null,
        message: userMessage,
        history: history,
      })

      const response = await fetch('/api/ai/chat', {
        method: 'POST',
        headers,
        body,
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

        // Parse complete SSE events from buffer
        const parts = buffer.split('\n\n')
        buffer = parts.pop() || ''

        for (const part of parts) {
          const lines = part.split('\n')
          let eventType = ''
          let eventData = ''

          for (const line of lines) {
            if (line.startsWith('event:')) {
              eventType = line.slice(6).trim()
            } else if (line.startsWith('data:')) {
              eventData = line.slice(5).trim()
            }
          }

          if (eventType === 'error') {
            throw new Error(eventData || 'Stream error')
          }
          if (eventType === 'token' && eventData) {
            assistantMsg.content += eventData
            currentToken.value = eventData
          }
          if (eventType === 'done') {
            // stream complete
          }
        }
      }
    } catch (e: any) {
      if (e.name === 'AbortError') {
        if (assistantMsg.content) {
          assistantMsg.content += '\n\n*[已取消]*'
        } else {
          messages.value.pop()
        }
      } else {
        error.value = e.message || 'Stream error'
        if (!assistantMsg.content) {
          messages.value.pop()
        } else {
          assistantMsg.content += '\n\n*[连接中断]*'
        }
      }
    } finally {
      streaming.value = false
      currentToken.value = ''
      abortController.value = null
    }
  }

  function abort() {
    if (abortController.value) {
      abortController.value.abort()
    }
  }

  function clear() {
    messages.value = []
    error.value = ''
  }

  return {
    messages: readonly(messages),
    streaming: readonly(streaming),
    currentToken: readonly(currentToken),
    error: readonly(error),
    send,
    abort,
    clear,
    updateOptions,
    addMessage,
  }
}
