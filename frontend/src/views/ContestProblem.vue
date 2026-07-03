<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getProblem, submitContestCode } from '../api'
import MonacoEditor from '../components/MonacoEditor.vue'
import GlowButton from '../components/GlowButton.vue'
import MarkdownRenderer from '../components/MarkdownRenderer.vue'

const route = useRoute()
const router = useRouter()
const contestId = Number(route.params.contestId)
const problemId = Number(route.params.problemId)
const problem = ref<any>(null)
const sampleCases = ref<any[]>([])
const language = ref('cpp')
const submitting = ref(false)

const langOptions = [
  { value: 'cpp', label: 'C++' },
  { value: 'python', label: 'Python' },
  { value: 'java', label: 'Java' },
  { value: 'rust', label: 'Rust' },
]

const templates: Record<string, string> = {
  cpp: '#include <iostream>\nusing namespace std;\n\nint main() {\n    // your code here\n    return 0;\n}',
  python: '# your code here',
  java: 'import java.util.*;\n\npublic class Main {\n    public static void main(String[] args) {\n        // your code here\n    }\n}',
  rust: 'fn main() {\n    // your code here\n}',
}

function loadDraft(): string {
  const key = `judgex-draft-contest-${contestId}-${problemId}-${language.value}`
  const draft = localStorage.getItem(key)
  return draft !== null ? draft : templates[language.value]
}

const code = ref(loadDraft())

function onCodeChange(value: string) {
  code.value = value
  const key = `judgex-draft-contest-${contestId}-${problemId}-${language.value}`
  localStorage.setItem(key, value)
}

watch(language, () => {
  code.value = loadDraft()
})

onMounted(async () => {
  try {
    const res = await getProblem(problemId)
    problem.value = res.data
    if (res.data.sample_cases) {
      sampleCases.value = typeof res.data.sample_cases === 'string'
        ? JSON.parse(res.data.sample_cases)
        : res.data.sample_cases
    }
  } catch {
    router.push('/contests')
  }
})

async function submit() {
  if (!code.value.trim()) return
  submitting.value = true
  try {
    const res = await submitContestCode(contestId, problemId, language.value, code.value)
    const key = `judgex-draft-contest-${contestId}-${problemId}-${language.value}`
    localStorage.removeItem(key)
    router.push(`/submissions/${res.data.submission_id}`)
  } catch (e: any) {
    ;(window as any).$toast?.error('Submit failed: ' + (e.response?.data?.error || e.message))
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div v-if="problem" class="flex h-[calc(100vh-3.5rem)]" v-icon-color>
    <!-- Left: Problem description -->
    <div class="w-1/2 overflow-y-auto border-r border-zinc-200 bg-white p-6 dark:bg-zinc-950 dark:border-zinc-800">
      <button
        class="mb-4 inline-flex items-center gap-1 text-sm font-medium text-brand-600 transition-colors hover:text-brand-700 dark:text-brand-400"
        @click="router.push(`/contests/${contestId}`)"
      >
        &larr; 返回比赛
      </button>

      <h1 class="mb-2 text-xl font-bold text-zinc-900 dark:text-zinc-100">{{ problem.title }}</h1>
      <div class="mb-6 flex gap-5 text-xs text-zinc-400">
        <span class="inline-flex items-center gap-1.5 rounded-lg bg-zinc-100 px-2.5 py-1 font-medium text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">
          <svg class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><path stroke-linecap="round" d="M12 6v6l4 2"/></svg>
          {{ problem.time_limit }} ms
        </span>
        <span class="inline-flex items-center gap-1.5 rounded-lg bg-zinc-100 px-2.5 py-1 font-medium text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">
          <svg class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><rect x="2" y="3" width="20" height="14" rx="2"/><path stroke-linecap="round" d="M8 21h8M12 17v4"/></svg>
          {{ problem.memory_limit }} MB
        </span>
      </div>

      <MarkdownRenderer :content="problem.description" />

      <div v-if="sampleCases.length > 0" class="mt-8 space-y-4">
        <h3 class="text-sm font-bold text-zinc-700 dark:text-zinc-300">Examples</h3>
        <div
          v-for="(tc, i) in sampleCases"
          :key="i"
          class="rounded-xl border border-zinc-200 bg-gradient-to-b from-zinc-50/80 to-zinc-50/30 p-4 shadow-sm dark:from-zinc-900/50 dark:to-zinc-900/20 dark:border-zinc-800"
        >
          <div class="mb-3">
            <div class="mb-1.5">
              <span class="rounded-lg bg-zinc-200 px-2 py-0.5 text-xs font-bold text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">Input {{ i + 1 }}</span>
            </div>
            <pre class="rounded-xl bg-zinc-900 px-4 py-3 text-sm text-zinc-100 shadow-lg shadow-zinc-900/10 dark:shadow-zinc-950/50">{{ tc.input }}</pre>
          </div>
          <div>
            <div class="mb-1.5">
              <span class="rounded-lg bg-zinc-200 px-2 py-0.5 text-xs font-bold text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">Output {{ i + 1 }}</span>
            </div>
            <pre class="rounded-xl bg-zinc-900 px-4 py-3 text-sm text-zinc-100 shadow-lg shadow-zinc-900/10 dark:shadow-zinc-950/50">{{ tc.output }}</pre>
          </div>
        </div>
      </div>
    </div>

    <!-- Right: Code editor -->
    <div class="flex w-1/2 flex-col bg-white dark:bg-zinc-950">
      <div class="flex items-center justify-between border-b border-zinc-200 px-4 py-3 dark:border-zinc-800">
        <select
          v-model="language"
          class="rounded-xl border border-zinc-200 bg-zinc-50 px-4 py-2 text-sm font-medium text-zinc-700 shadow-sm transition-all duration-200 focus:border-brand-500 focus:outline-none focus:ring-2 focus:ring-brand-500/15 dark:bg-zinc-900 dark:text-zinc-300 dark:border-zinc-700"
        >
          <option v-for="opt in langOptions" :key="opt.value" :value="opt.value">
            {{ opt.label }}
          </option>
        </select>

        <GlowButton
          class="px-5 py-2 text-sm font-semibold gap-1.5"
          :disabled="submitting"
          @click="submit"
        >
          <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7"/>
          </svg>
          {{ submitting ? '提交中...' : '提交' }}
        </GlowButton>
      </div>

      <div class="flex-1">
        <MonacoEditor :model-value="code" :language="language" @update:model-value="onCodeChange" />
      </div>
    </div>
  </div>

  <div v-else class="flex items-center justify-center py-32 text-sm text-zinc-400">
    Loading...
  </div>
</template>
