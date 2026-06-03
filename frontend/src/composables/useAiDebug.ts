import { ref, readonly } from 'vue'

export interface DebugTestResult {
  case_id: number
  input: string
  expected: string
  actual: string
  passed: boolean
  status: string
  time_used: number
  error_msg: string
}

export interface DebugState {
  phase: string // 'loading' | 'testing' | 'analyzing' | 'extracting' | 'verifying' | 'done' | 'error'
  statusMessage: string
  testResults: DebugTestResult[] | null
  passedCount: number
  totalCount: number
  analysis: string
  fixedCode: string
  verificationResults: DebugTestResult[] | null
  verifyPassed: number
  verifyTotal: number
  error: string
}

export function useAiDebug() {
  const state = ref<DebugState>({
    phase: '',
    statusMessage: '',
    testResults: null,
    passedCount: 0,
    totalCount: 0,
    analysis: '',
    fixedCode: '',
    verificationResults: null,
    verifyPassed: 0,
    verifyTotal: 0,
    error: '',
  })

  const streaming = ref(false)
  const abortController = ref<AbortController | null>(null)

  function reset() {
    state.value = {
      phase: '',
      statusMessage: '',
      testResults: null,
      passedCount: 0,
      totalCount: 0,
      analysis: '',
      fixedCode: '',
      verificationResults: null,
      verifyPassed: 0,
      verifyTotal: 0,
      error: '',
    }
  }

  async function startDebug(problemId: number, code: string, language: string): Promise<void> {
    reset()
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

      const response = await fetch('/api/ai/debug', {
        method: 'POST',
        headers,
        body: JSON.stringify({
          problem_id: problemId,
          language,
          code,
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
        state.value.statusMessage = '已取消'
      } else {
        state.value.phase = 'error'
        state.value.error = e.message || 'Debug agent error'
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
        if (data.includes('加载题目')) state.value.phase = 'loading'
        else if (data.includes('加载测试') || data.includes('加载提交')) state.value.phase = 'loading'
        else if (data.includes('评测用户代码')) {
          state.value.phase = 'testing'
          const m = data.match(/(\d+)/)
          if (m) state.value.totalCount = parseInt(m[1])
        } else if (data.includes('AI 正在分析') || data.includes('分析错误')) state.value.phase = 'analyzing'
        else if (data.includes('提取修复') || data.includes('未生成')) state.value.phase = 'extracting'
        else if (data.includes('验证修复')) state.value.phase = 'verifying'
        else if (data.includes('通过') || data.includes('修复成功') || data.includes('失败')) state.value.phase = 'done'
        break

      case 'test_results':
        try {
          const tr = JSON.parse(data)
          state.value.testResults = tr.test_results
          state.value.passedCount = tr.passed || 0
          state.value.totalCount = tr.total || 0
          state.value.phase = 'testing'
        } catch { /* ignore */ }
        break

      case 'token':
        state.value.analysis += data
        break

      case 'fix':
        state.value.fixedCode = data
        state.value.phase = 'extracting'
        break

      case 'verification':
        try {
          const vr = JSON.parse(data)
          state.value.verificationResults = vr.test_results
          state.value.verifyPassed = vr.passed || 0
          state.value.verifyTotal = vr.total || 0
          state.value.phase = 'done'
        } catch { /* ignore */ }
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
    startDebug,
    abort,
    reset,
  }
}
