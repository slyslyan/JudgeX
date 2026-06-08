<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, nextTick, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getSubmission, rejudgeSubmission, type Submission } from '../api'
import hljs from 'highlight.js'
import AiChat from '../components/AiChat.vue'
import DebugAgent from '../components/DebugAgent.vue'

const route = useRoute()
const router = useRouter()
const submission = ref<Submission | null>(null)
const rejudging = ref(false)
const codeBlock = ref<HTMLElement | null>(null)

const showAiChat = ref(false)
const aiReveal = ref<{ agentType?: string; message?: string } | null>(null)

const showAiDebug = ref(false)

const errorStatuses = ['Wrong Answer', 'Time Limit Exceeded', 'Memory Limit Exceeded', 'Runtime Error', 'Compile Error']
function isErrorStatus(s: string) { return errorStatuses.includes(s) }

const isAdmin = computed(() => {
  const u = localStorage.getItem('user')
  if (!u) return false
  const role = JSON.parse(u).role
  return role === 'admin' || role === 'super_admin'
})

function openAiDiagnose() {
  showAiChat.value = true
  nextTick().then(() => {
    aiReveal.value = { agentType: 'diagnose', message: `为什么得到 ${submission.value?.status}?` }
  })
}

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
  <div v-if="submission" class="mx-auto max-w-6xl px-6 py-8">
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
        v-if="isErrorStatus(submission.status) && !showAiChat"
        class="inline-flex items-center gap-1.5 rounded-xl border border-brand-200 bg-gradient-to-br from-brand-50 to-brand-100 px-3.5 py-2 text-sm font-medium text-brand-700 shadow-sm transition-all duration-200 hover:shadow-md dark:from-brand-900/20 dark:to-brand-900/30 dark:border-brand-700 dark:text-brand-400"
        @click="openAiDiagnose"
      >
        AI 诊断
      </button>
      <button
        class="inline-flex items-center gap-1.5 rounded-xl border border-brand-200 bg-gradient-to-br from-brand-50 to-brand-100 px-3.5 py-2 text-sm font-medium text-brand-700 shadow-sm transition-all duration-200 hover:shadow-md dark:from-brand-900/20 dark:to-brand-900/30 dark:border-brand-800 dark:text-brand-400"
        @click="showAiDebug = !showAiDebug"
      >
        <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 00-3.09 3.09zM18.259 8.715L18 9.75l-.259-1.035a4.5 4.5 0 00-3.09-3.09L13.5 5.25l1.035-.259a4.5 4.5 0 003.09-3.09L18 .75l.259 1.035a4.5 4.5 0 003.09 3.09L22.5 5.25l-1.035.259a4.5 4.5 0 00-3.09 3.09z"/></svg>
        AI Debug
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

    <!-- Code + AI 诊断 split -->
    <div class="flex gap-4 min-h-0">
      <div :class="showAiChat ? 'flex-1 min-w-0' : 'flex-1'">
        <div class="overflow-hidden rounded-2xl border border-zinc-200 bg-zinc-900 shadow-lg shadow-zinc-900/10 dark:border-zinc-700 dark:shadow-zinc-950/50">
          <div class="flex items-center gap-2 border-b border-zinc-700/50 px-5 py-2.5">
            <span class="h-3 w-3 rounded-full bg-red-400/80 shadow-sm shadow-red-400/50" />
            <span class="h-3 w-3 rounded-full bg-amber-400/80 shadow-sm shadow-amber-400/50" />
            <span class="h-3 w-3 rounded-full bg-emerald-400/80 shadow-sm shadow-emerald-400/50" />
            <span class="ml-3 text-xs text-zinc-500">solution.{{ submission.language }}</span>
          </div>
          <div class="p-5">
            <pre class="overflow-auto text-sm leading-relaxed"><code ref="codeBlock" :class="submission.language === 'python' ? 'language-python' : submission.language === 'java' ? 'language-java' : submission.language === 'go' ? 'language-go' : submission.language === 'rust' ? 'language-rust' : 'language-cpp'">{{ submission.code }}</code></pre>
          </div>
        </div>
      </div>

      <div v-if="showAiDebug" class="w-96 shrink-0">
        <DebugAgent
          :problem-id="submission.problem_id"
          :code="submission.code"
          :language="submission.language"
          @close="showAiDebug = false"
        />
      </div>

      <div v-if="showAiChat" class="w-96 shrink-0">
        <AiChat
          variant="inline"
          :options="{
            agentType: 'diagnose',
            problemId: submission.problem_id,
            submissionId: submission.id,
          }"
          :reveal="aiReveal"
          :suggestions="[
            '为什么得到 ' + submission.status + '?',
            '我遗漏了哪些边界条件？',
            '我的方法是否有逻辑错误？',
            '如何优化时间复杂度？'
          ]"
          @close="showAiChat = false"
        />
      </div>
    </div>
  </div>

  <div v-else class="flex items-center justify-center py-32 text-sm text-zinc-400">
    Loading...
  </div>
</template>
