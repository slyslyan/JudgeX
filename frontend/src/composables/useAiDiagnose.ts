import { ref, readonly } from 'vue'

export interface AiDiagnoseState {
  status: string
  analysis: string
  traceOutput: string
  error: string
  phase: 'idle' | 'running' | 'done' | 'error'
}

export interface AiDiagnoseOptions {
  problemId: number
  language: string
  code: string
  verdict: string
  compileError?: string
  timeUsed?: number
  failedInput?: string
  failedExpected?: string
  failedActual?: string
  failedCaseId?: number
}

export function useAiDiagnose() {
  const state = ref<AiDiagnoseState>({
    status: '',
    analysis: '',
    traceOutput: '',
    error: '',
    phase: 'idle',
  })
  const streaming = ref(false)
  const abortController = ref<AbortController | null>(null)

  function reset() {
    state.value = {
      status: '',
      analysis: '',
      traceOutput: '',
      error: '',
      phase: 'idle',
    }
  }

  async function start(options: AiDiagnoseOptions): Promise<void> {
    reset()
    streaming.value = true
    state.value.phase = 'running'

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

      const response = await fetch('/api/ai/diagnose', {
        method: 'POST',
        headers,
        body: JSON.stringify({
          problem_id: options.problemId,
          language: options.language,
          code: options.code,
          verdict: options.verdict,
          compile_error: options.compileError || '',
          time_used: options.timeUsed || 0,
          failed_input: options.failedInput || '',
          failed_expected: options.failedExpected || '',
          failed_actual: options.failedActual || '',
          failed_case_id: options.failedCaseId || 0,
        }),
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
        state.value.status = '已取消'
      } else if (e.message?.includes('503')) {
        state.value.phase = 'error'
        state.value.error = '诊断队列已满，请稍后再试'
      } else {
        state.value.phase = 'error'
        state.value.error = e.message || 'AI 诊断助手错误'
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
        state.value.status = data
        break
      case 'token':
        state.value.analysis += data
        break
      case 'trace':
        try {
          state.value.traceOutput = JSON.parse(data)
        } catch {
          state.value.traceOutput = data
        }
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
        if (state.value.phase !== 'error') {
          state.value.phase = 'done'
        }
        break
    }
  }

  return {
    state: readonly(state),
    streaming: readonly(streaming),
    start,
    abort,
    reset,
  }
}
