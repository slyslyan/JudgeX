<script setup lang="ts">
import { ref, onMounted, watch, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getProblem, getLastCode, getTemplates, runCode, type Problem, type RunResult } from '../api'
import MonacoEditor from '../components/MonacoEditor.vue'

const route = useRoute()
const router = useRouter()
const problem = ref<Problem | null>(null)
const language = ref('cpp')

const langOptions = [
  { value: 'cpp', label: 'C++' },
  { value: 'python', label: 'Python' },
  { value: 'java', label: 'Java' },
  { value: 'go', label: 'Go' },
  { value: 'rust', label: 'Rust' },
]

const defaultTemplates: Record<string, string> = {
  cpp: '#include <iostream>\nusing namespace std;\n\nint main() {\n    // your code here\n    return 0;\n}',
  python: '# your code here',
  java: 'import java.util.*;\n\npublic class Main {\n    public static void main(String[] args) {\n        // your code here\n    }\n}',
  go: 'package main\n\nimport "fmt"\n\nfunc main() {\n    // your code here\n}',
  rust: 'fn main() {\n    // your code here\n}',
}

const problemId = computed(() => Number(route.params.id))
const lastCode = ref<string | null>(null)
const lastCodeLang = ref<string | null>(null)
const userTemplates = ref<Record<string, string>>({})

function getCodeForLanguage(lang: string): string {
  const key = `judgex-draft-${problemId.value}-${lang}`
  const draft = localStorage.getItem(key)
  if (draft !== null) return draft
  if (lastCode.value && lastCodeLang.value === lang) return lastCode.value
  if (userTemplates.value[lang]) return userTemplates.value[lang]
  return defaultTemplates[lang]
}

const code = ref('')

function onCodeChange(value: string) {
  code.value = value
  const key = `judgex-draft-${problemId.value}-${language.value}`
  localStorage.setItem(key, value)
}

watch(language, (lang) => {
  code.value = getCodeForLanguage(lang)
})

onMounted(async () => {
  try {
    const res = await getProblem(problemId.value)
    problem.value = res.data
  } catch {
    router.push('/problems')
    return
  }

  const [lcRes, tmplRes] = await Promise.allSettled([
    getLastCode(problemId.value),
    getTemplates(),
  ])

  if (lcRes.status === 'fulfilled' && lcRes.value.data.code) {
    lastCode.value = lcRes.value.data.code
    lastCodeLang.value = lcRes.value.data.language
    language.value = lcRes.value.data.language!
  }

  if (tmplRes.status === 'fulfilled' && tmplRes.value.data.templates) {
    userTemplates.value = tmplRes.value.data.templates
  }

  code.value = getCodeForLanguage(language.value)
})

const debugInput = ref('')
const debugExpected = ref('')
const runningCode = ref(false)
const runResult = ref<RunResult | null>(null)
const outputMatch = ref<boolean | null>(null)

const sampleCases = computed(() => {
  if (!problem.value?.sample_cases) return []
  const sc = problem.value.sample_cases
  return Array.isArray(sc) ? sc : []
})

function loadSampleInput() {
  if (sampleCases.value.length > 0) {
    debugInput.value = sampleCases.value[0].input
    debugExpected.value = sampleCases.value[0].output
  }
}

async function handleRun() {
  runningCode.value = true
  runResult.value = null
  outputMatch.value = null
  try {
    const res = await runCode(
      language.value,
      code.value,
      debugInput.value,
      problem.value?.time_limit || 5000,
      problem.value?.memory_limit || 256
    )
    runResult.value = res.data
    if (debugExpected.value.trim() && res.data.stdout.trim()) {
      const normalize = (s: string) => s.trim().replace(/\r\n/g, '\n').replace(/[ \t]+$/gm, '').replace(/\n+$/, '')
      outputMatch.value = normalize(res.data.stdout) === normalize(debugExpected.value)
    }
  } catch (e: any) {
    runResult.value = {
      status: 'Error',
      stdout: '',
      stderr: e.response?.data?.error || e.message || 'Run failed',
      time_used: 0,
      memory_used: 0,
    }
  } finally {
    runningCode.value = false
  }
}

function goBack() {
  router.push(`/problems/${problemId.value}`)
}
</script>

<template>
  <div v-if="problem" class="flex h-[calc(100vh-3.5rem)] flex-col bg-white dark:bg-zinc-950">
    <!-- Top bar -->
    <div class="flex shrink-0 items-center gap-4 border-b border-zinc-200 px-6 py-3 dark:border-zinc-800">
      <button
        class="inline-flex items-center gap-1.5 rounded-xl border border-zinc-200 bg-zinc-50 px-4 py-2 text-sm font-medium text-zinc-600 transition-all duration-200 hover:bg-zinc-100 hover:text-zinc-800 dark:bg-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-200 dark:border-zinc-700"
        @click="goBack"
      >
        <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M15 19l-7-7 7-7"/></svg>
        返回题目
      </button>

      <span class="text-sm font-semibold text-zinc-700 dark:text-zinc-300">{{ problem.title }}</span>

      <div class="ml-auto flex items-center gap-3">
        <select
          v-model="language"
          class="rounded-xl border border-zinc-200 bg-zinc-50 px-4 py-2 text-sm font-medium text-zinc-700 shadow-sm transition-all duration-200 focus:border-brand-500 focus:outline-none focus:ring-2 focus:ring-brand-500/15 dark:bg-zinc-900 dark:text-zinc-300 dark:border-zinc-700"
        >
          <option v-for="opt in langOptions" :key="opt.value" :value="opt.value">
            {{ opt.label }}
          </option>
        </select>

        <button
          class="btn-gradient"
          @click="handleRun"
          :disabled="runningCode"
        >
          {{ runningCode ? 'Running...' : 'Run' }}
        </button>
      </div>
    </div>

    <!-- Main content: editor + debug side by side -->
    <div class="flex flex-1 min-h-0">
      <!-- Code editor -->
      <div class="flex w-3/5 min-h-0 flex-col border-r border-zinc-200 dark:border-zinc-800">
        <MonacoEditor :model-value="code" :language="language" @update:model-value="onCodeChange" />
      </div>

      <!-- Debug panel -->
      <div class="flex w-2/5 min-h-0 flex-col bg-zinc-50/50 dark:bg-zinc-900/30">
        <div class="flex shrink-0 items-center gap-2 border-b border-zinc-200 px-4 py-2.5 dark:border-zinc-800">
          <span class="text-xs font-bold text-zinc-500 uppercase tracking-wider">Test Console</span>
          <button class="rounded-lg px-3 py-1 text-xs font-medium text-zinc-500 transition-all duration-200 hover:bg-zinc-200 hover:text-zinc-700 dark:hover:bg-zinc-800 dark:hover:text-zinc-300" @click="loadSampleInput">Load Sample</button>
        </div>

        <div class="flex flex-1 min-h-0 flex-col gap-4 overflow-y-auto p-4">
          <div class="flex flex-col">
            <div class="mb-1.5 text-xs font-semibold text-zinc-400 uppercase tracking-wider">Input</div>
            <textarea
              v-model="debugInput"
              rows="10"
              class="input-glow w-full resize-none rounded-xl border border-zinc-200 bg-white px-4 py-3 text-sm font-mono text-zinc-700 placeholder:text-zinc-400 dark:bg-zinc-950 dark:text-zinc-300 dark:border-zinc-700"
              placeholder="在此粘贴测试输入..."
            ></textarea>
          </div>

          <div class="flex flex-col">
            <div class="mb-1.5 text-xs font-semibold text-zinc-400 uppercase tracking-wider">Expected Output</div>
            <textarea
              v-model="debugExpected"
              rows="4"
              class="input-glow w-full resize-none rounded-xl border border-zinc-200 bg-white px-4 py-3 text-sm font-mono text-zinc-600 placeholder:text-zinc-400 dark:bg-zinc-950 dark:text-zinc-300 dark:border-zinc-700"
              placeholder="（选填）"
            ></textarea>
          </div>

          <div class="flex flex-1 flex-col min-h-0">
            <div class="mb-1.5 text-xs font-semibold text-zinc-400 uppercase tracking-wider">Output</div>
            <div
              v-if="runningCode"
              class="flex flex-1 items-center justify-center rounded-xl border border-zinc-200 bg-white text-sm text-zinc-400 dark:bg-zinc-950 dark:border-zinc-700"
            >
              Running...
            </div>
            <pre
              v-else-if="runResult"
              :class="[
                'flex-1 overflow-auto rounded-xl border p-4 text-sm font-mono',
                outputMatch === true
                  ? 'border-emerald-200 bg-emerald-50/50 text-emerald-800 dark:bg-emerald-900/20 dark:border-emerald-800 dark:text-emerald-400'
                  : outputMatch === false
                    ? 'border-red-200 bg-red-50/50 text-red-800 dark:bg-red-900/20 dark:border-red-800 dark:text-red-400'
                    : runResult.status !== 'Accepted' && runResult.status !== 'Error'
                      ? 'border-red-200 bg-red-50/50 text-red-800 dark:bg-red-900/20 dark:border-red-800 dark:text-red-400'
                      : 'border-zinc-200 bg-white text-zinc-700 dark:bg-zinc-950 dark:border-zinc-700 dark:text-zinc-300'
              ]"
            >{{ runResult.stdout || runResult.stderr || '（无输出）' }}</pre>
            <div
              v-else
              class="flex flex-1 items-center justify-center rounded-xl border border-zinc-200 bg-white text-sm text-zinc-400 dark:bg-zinc-950 dark:border-zinc-700"
            >
              点击"运行"执行代码
            </div>
          </div>
        </div>

        <div v-if="runResult" class="flex shrink-0 items-center gap-4 border-t border-zinc-200 px-4 py-2.5 dark:border-zinc-800">
          <template v-if="outputMatch !== null">
            <span
              :class="[
                'inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs font-semibold shadow-sm',
                outputMatch
                  ? 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400 dark:border-emerald-800'
                  : 'bg-red-50 text-red-700 border-red-200 dark:bg-red-900/30 dark:text-red-400 dark:border-red-800'
              ]"
            >
              {{ outputMatch ? 'Output Match' : 'Output Mismatch' }}
            </span>
          </template>
          <template v-else>
            <span
              :class="[
                'inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs font-semibold shadow-sm',
                runResult.status === 'Accepted'
                  ? 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400 dark:border-emerald-800'
                  : runResult.status === 'Compile Error'
                    ? 'bg-pink-50 text-pink-700 border-pink-200 dark:bg-pink-900/30 dark:text-pink-400 dark:border-pink-800'
                    : 'bg-red-50 text-red-700 border-red-200 dark:bg-red-900/30 dark:text-red-400 dark:border-red-800'
              ]"
            >
              {{ runResult.status === 'Accepted' ? 'Completed' : runResult.status }}
            </span>
          </template>
          <span class="text-xs text-zinc-400">Time: {{ runResult.time_used }} ms</span>
          <span class="text-xs text-zinc-400">Memory: {{ runResult.memory_used }} KB</span>
        </div>
      </div>
    </div>
  </div>

  <div v-else class="flex items-center justify-center py-32 text-sm text-zinc-400">
    Loading...
  </div>
</template>
