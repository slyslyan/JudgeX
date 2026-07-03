<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, nextTick, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getSubmission, rejudgeSubmission, type Submission } from '../api'
import hljs from 'highlight.js'
import AiDiagnoseAssistant from '../components/AiDiagnoseAssistant.vue'

const route = useRoute()
const router = useRouter()
const submission = ref<Submission | null>(null)
const rejudging = ref(false)
const codeBlock = ref<HTMLElement | null>(null)

const showAiDiagnose = ref(false)

const errorStatuses = ['Wrong Answer', 'Time Limit Exceeded', 'Memory Limit Exceeded', 'Runtime Error', 'Compile Error']
function isErrorStatus(s: string) { return errorStatuses.includes(s) }

const isAdmin = computed(() => {
  const u = localStorage.getItem('user')
  if (!u) return false
  const role = JSON.parse(u).role
  return role === 'admin' || role === 'super_admin'
})

async function doRejudge() {
  if (!submission.value) return
  rejudging.value = true
  try {
    await rejudgeSubmission(submission.value.id)
    ;(window as any).$toast?.success('评测中...')
    submission.value!.status = 'pending'
    connectSSE(submission.value!.id)
  } catch (e: any) {
    ;(window as any).$toast?.error(e.response?.data?.error || 'Re-judge failed')
  } finally {
    rejudging.value = false
  }
}

const statusStyle = (s: string) => {
  if (s === 'Accepted') return 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400 dark:border-emerald-800'
  if (s === 'Wrong Answer') return 'bg-red-50 text-red-700 border-red-200 dark:bg-red-900/30 dark:text-red-400 dark:border-red-800'
  if (s === 'Time Limit Exceeded') return 'bg-amber-50 text-amber-700 border-amber-200 dark:bg-amber-900/30 dark:text-amber-400 dark:border-amber-800'
  if (s === 'Runtime Error') return 'bg-pink-50 text-pink-700 border-pink-200 dark:bg-pink-900/30 dark:text-pink-400 dark:border-pink-800'
  if (s === 'Compile Error') return 'bg-pink-50 text-pink-700 border-pink-200 dark:bg-pink-900/30 dark:text-pink-400 dark:border-pink-800'
  if (s === 'pending' || s === 'judging') return 'bg-blue-50 text-blue-700 border-blue-200 dark:bg-blue-900/30 dark:text-blue-400 dark:border-blue-800'
  return 'bg-zinc-50 text-zinc-600 border-zinc-200 dark:bg-zinc-800 dark:text-zinc-400 dark:border-zinc-700'
}

function connectSSE(id: number) {
  const token = localStorage.getItem('token')
  const url = `/api/submissions/${id}/events`
  const headers: Record<string, string> = {}
  if (token) headers['Authorization'] = `Bearer ${token}`

  fetch(url, { headers })
    .then(response => {
      if (!response.ok) throw new Error('SSE connection failed')
      const reader = response.body!.getReader()
      const decoder = new TextDecoder()
      let buffer = ''

      function read() {
        reader.read().then(({ done, value }) => {
          if (done) return
          buffer += decoder.decode(value, { stream: true })
          const lines = buffer.split('\n')
          buffer = lines.pop() || ''
          for (const line of lines) {
            if (line.startsWith('data: ')) {
              try {
                const data = JSON.parse(line.slice(6))
                submission.value = { ...submission.value, ...data }
                if (data.status !== 'pending' && data.status !== 'judging') {
                  reader.cancel()
                  return
                }
              } catch (e) { console.error('SSE parse error:', e) }
            }
          }
          read()
        }).catch((e) => { console.error('SSE read error:', e) })
      }
      read()
    })
    .catch((e) => { console.error('SSE fetch error:', e) })
}

onMounted(async () => {
  const id = Number(route.params.id)
  if (!id || isNaN(id)) {
    router.push('/submissions')
    return
  }
  try {
    const res = await getSubmission(id)
    submission.value = res.data
    if (res.data.status === 'pending' || res.data.status === 'judging') {
      connectSSE(id)
    }
  } catch {
    router.push('/submissions')
  }
})

onBeforeUnmount(() => {
  // SSE reader cancels itself
})

watch(submission, async () => {
  await nextTick()
  if (codeBlock.value) {
    hljs.highlightElement(codeBlock.value)
  }
})
</script>

<template>
  <div v-if="submission" class="mx-auto max-w-6xl px-6 py-8" v-icon-color>
    <div class="mb-4 flex items-center gap-3">
      <button
        class="inline-flex items-center gap-1.5 rounded-xl px-3 py-2 text-sm font-medium text-zinc-500 transition-all duration-200 hover:bg-zinc-100 hover:text-zinc-700 dark:hover:bg-zinc-800 dark:hover:text-zinc-300"
        @click="router.push('/submissions')"
      >
        &larr; 返回提交记录
      </button>

      <button
        v-if="isAdmin"
        :disabled="rejudging"
        class="inline-flex items-center gap-1.5 rounded-xl border border-amber-200 bg-gradient-to-br from-amber-50 to-amber-100 px-3.5 py-2 text-sm font-medium text-amber-700 shadow-sm transition-all duration-200 hover:shadow-md disabled:opacity-50 dark:from-amber-900/20 dark:to-amber-900/30 dark:border-amber-800 dark:text-amber-400"
        @click="doRejudge"
      >
        {{ rejudging ? '评测中...' : 'Re-judge' }}
      </button>

      <button
        v-if="isErrorStatus(submission.status)"
        class="inline-flex items-center gap-1.5 rounded-xl border border-amber-200 bg-gradient-to-br from-amber-50 to-orange-50 px-3.5 py-2 text-sm font-medium text-amber-700 shadow-sm transition-all duration-200 hover:shadow-md dark:from-amber-900/20 dark:to-amber-900/30 dark:border-amber-700 dark:text-amber-400"
        @click="showAiDiagnose = !showAiDiagnose"
      >
        <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M4.26 10.147a60.438 60.438 0 0 0-.491 6.347A48.62 48.62 0 0 1 12 20.904a48.62 48.62 0 0 1 8.232-4.41 60.46 60.46 0 0 0-.491-6.347m-15.482 0a50.636 50.636 0 0 0-2.658-.813A59.906 59.906 0 0 1 12 3.493a59.903 59.903 0 0 1 10.399 5.84c-.896.248-1.783.52-2.658.814m-15.482 0A50.717 50.717 0 0 1 12 13.489a50.702 50.702 0 0 1 7.74-3.342" /></svg>
        AI 诊断
      </button>
    </div>

    <h2 class="mb-6 text-xl font-bold text-zinc-900 dark:text-zinc-100">
      提交 #{{ submission.id }}
    </h2>

    <!-- Info cards with depth -->
    <div class="mb-6 grid grid-cols-3 gap-4">
      <div v-for="item in [
        { label: 'Problem', value: submission.problem_title || `#${submission.problem_id}` },
        { label: 'Language', value: submission.language },
        { label: 'Status', value: '', isStatus: true },
        { label: 'Time', value: submission.time_used + ' ms' },
        { label: 'Memory', value: submission.memory_used + ' KB' },
        { label: 'Submitted', value: new Date(submission.created_at).toLocaleString() },
      ]" :key="item.label" class="card-premium p-4">
        <div class="text-xs font-medium text-zinc-400 uppercase tracking-wider mb-1">{{ item.label }}</div>
        <div v-if="!item.isStatus" class="text-sm font-semibold text-zinc-800 dark:text-zinc-200">{{ item.value }}</div>
        <span v-else :class="statusStyle(submission.status)" class="inline-flex rounded-full border px-2.5 py-0.5 text-xs font-semibold">
          {{ submission.status }}
        </span>
      </div>
    </div>

    <!-- Test case progress -->
    <div
      v-if="submission.total_cases > 0 && submission.status !== 'Accepted'"
      class="mb-5 rounded-xl border border-amber-200 bg-gradient-to-r from-amber-50 to-amber-50/50 p-5 shadow-sm dark:from-amber-900/20 dark:to-amber-900/10 dark:border-amber-800"
    >
      <div class="flex items-center justify-between mb-2">
        <p class="text-sm font-semibold text-amber-800 dark:text-amber-400">
          测试用例进度
        </p>
        <span class="text-sm font-mono font-bold text-amber-700 dark:text-amber-400">
          {{ submission.passed_count }} / {{ submission.total_cases }}
        </span>
      </div>
      <div class="h-2.5 w-full rounded-full bg-amber-200/50 dark:bg-amber-900/40 overflow-hidden">
        <div
          class="h-2.5 rounded-full bg-gradient-to-r from-amber-400 to-amber-500 transition-all duration-700 ease-out shadow-sm shadow-amber-300/50"
          :style="{ width: (submission.passed_count / submission.total_cases * 100) + '%' }"
        />
      </div>
    </div>

    <!-- Code + AI 诊断助手 split -->
    <div class="flex gap-4 min-h-0">
      <div class="flex-1">
        <div class="overflow-hidden rounded-2xl border border-zinc-200 bg-zinc-900 shadow-lg shadow-zinc-900/10 dark:border-zinc-700 dark:shadow-zinc-950/50">
          <div class="flex items-center gap-2 border-b border-zinc-700/50 px-5 py-2.5">
            <span class="h-3 w-3 rounded-full bg-red-400/80 shadow-sm shadow-red-400/50" />
            <span class="h-3 w-3 rounded-full bg-amber-400/80 shadow-sm shadow-amber-400/50" />
            <span class="h-3 w-3 rounded-full bg-emerald-400/80 shadow-sm shadow-emerald-400/50" />
            <span class="ml-3 text-xs text-zinc-500">solution.{{ submission.language }}</span>
          </div>
          <div class="p-5">
            <pre class="overflow-auto text-sm leading-relaxed"><code ref="codeBlock" :class="submission.language === 'python' ? 'language-python' : submission.language === 'java' ? 'language-java' : submission.language === 'rust' ? 'language-rust' : 'language-cpp'">{{ submission.code }}</code></pre>
          </div>
        </div>
      </div>

      <Transition name="slide-up">
        <div v-if="showAiDiagnose" key="ai-diagnose" class="w-96 shrink-0">
          <AiDiagnoseAssistant
            :problem-id="submission.problem_id"
            :code="submission.code"
            :language="submission.language"
            :verdict="submission.status === 'Compile Error' ? 'CE' : submission.status === 'Time Limit Exceeded' ? 'TLE' : submission.status === 'Wrong Answer' ? 'WA' : 'RE'"
            :compile-error="submission.error_message"
            :time-used="submission.time_used"
            @close="showAiDiagnose = false"
          />
        </div>
      </Transition>
    </div>
  </div>

  <div v-else class="flex items-center justify-center py-32 text-sm text-zinc-400">
    Loading...
  </div>
</template>
