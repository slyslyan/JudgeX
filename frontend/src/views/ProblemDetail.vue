<script setup lang="ts">
import { ref, onMounted, watch, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getProblem, submitCode, getLastCode, getTemplates, runCode, type Problem, type RunResult } from '../api'
import MonacoEditor from '../components/MonacoEditor.vue'
import GlowButton from '../components/GlowButton.vue'
import MarkdownRenderer from '../components/MarkdownRenderer.vue'

const route = useRoute()
const router = useRouter()
const problem = ref<Problem | null>(null)
const language = ref('cpp')
const submitting = ref(false)

const langOptions = [
  { value: 'cpp', label: 'C++' },
  { value: 'python', label: 'Python' },
  { value: 'java', label: 'Java' },
  { value: 'rust', label: 'Rust' },
]

const copiedId = ref<string | null>(null)

async function copyToClipboard(text: string, id: string) {
  try {
    await navigator.clipboard.writeText(text)
    copiedId.value = id
    setTimeout(() => { copiedId.value = null }, 1500)
  } catch (e) {
    console.error('Clipboard fallback:', e)
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    document.execCommand('copy')
    document.body.removeChild(ta)
    copiedId.value = id
    setTimeout(() => { copiedId.value = null }, 1500)
  }
}

const defaultTemplates: Record<string, string> = {
  cpp: '#include <iostream>\nusing namespace std;\n\nint main() {\n    // your code here\n    return 0;\n}',
  python: '# your code here',
  java: 'import java.util.*;\n\npublic class Main {\n    public static void main(String[] args) {\n        // your code here\n    }\n}',
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
  } catch (e) {
    console.error('Failed to load problem:', e)
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

const showTags = ref(false)
const showDebug = ref(true)
const debugInput = ref('')
const debugExpected = ref('')
const runningCode = ref(false)
const runResult = ref<RunResult | null>(null)
const outputMatch = ref<boolean | null>(null)

function loadSampleInput() {
  if (sampleCases.value.length > 0) {
    debugInput.value = sampleCases.value[0].input
    debugExpected.value = sampleCases.value[0].output
  }
}

async function handleRun() {
  runningCode.value = true
  showDebug.value = true
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

const sampleCases = computed(() => {
  if (!problem.value?.sample_cases) return []
  const sc = problem.value.sample_cases
  return Array.isArray(sc) ? sc : []
})

async function submit() {
  if (!code.value.trim()) return
  submitting.value = true
  try {
    const res = await submitCode(problemId.value, language.value, code.value)
    const key = `judgex-draft-${problemId.value}-${language.value}`
    localStorage.removeItem(key)
    if (res.data.cached) {
      ;(window as any).$toast?.info('已经提交过。显示提交记录。')
      router.push('/submissions')
      return
    }
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
    <div class="w-1/2 flex flex-col border-r border-zinc-200 bg-white dark:bg-zinc-950 dark:border-zinc-800">
      <div class="shrink-0 p-6 pb-3">
        <h1 class="mb-2 text-xl font-bold text-zinc-900 dark:text-zinc-100">{{ problem.title }}</h1>
        <div class="relative flex items-center gap-5 text-xs text-zinc-400">
          <span class="inline-flex items-center gap-1.5 rounded-lg bg-zinc-100 px-2.5 py-1 font-medium text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">
            <svg class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><path stroke-linecap="round" d="M12 6v6l4 2"/></svg>
            {{ problem.time_limit }} ms
          </span>
          <span class="inline-flex items-center gap-1.5 rounded-lg bg-zinc-100 px-2.5 py-1 font-medium text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">
            <svg class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><rect x="2" y="3" width="20" height="14" rx="2"/><path stroke-linecap="round" d="M8 21h8M12 17v4"/></svg>
            {{ problem.memory_limit }} MB
          </span>
          <router-link
            :to="`/problems/${problem.id}/code`"
            class="ml-auto rounded-xl border border-zinc-200 bg-white px-4 py-1.5 text-xs font-semibold text-zinc-600 shadow-sm transition-all duration-200 hover:shadow-md hover:scale-105 hover:text-zinc-800 dark:bg-zinc-900 dark:text-zinc-400 dark:border-zinc-700 dark:hover:text-zinc-200"
          >
            全屏编辑器
          </router-link>
        </div>

        <!-- Tags row: hidden by default, click to reveal -->
        <div v-if="problem.tags && problem.tags.length > 0" class="mt-2 flex items-center gap-2">
          <button
            class="inline-flex items-center gap-1 rounded-lg px-2 py-1 text-xs font-medium transition-all duration-200"
            :class="showTags
              ? 'bg-brand-100 text-brand-700 dark:bg-brand-900/40 dark:text-brand-400'
              : 'bg-zinc-100 text-zinc-500 hover:bg-zinc-200 hover:text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400 dark:hover:bg-zinc-700'"
            @click="showTags = !showTags"
          >
            <svg class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" d="M9.568 3H5.25A2.25 2.25 0 0 0 3 5.25v4.318c0 .597.237 1.17.659 1.591l9.581 9.581c.699.699 1.78.872 2.607.33a18.095 18.095 0 0 0 5.223-5.223c.542-.827.369-1.908-.33-2.607L11.16 3.66A2.25 2.25 0 0 0 9.568 3Z"/>
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 6h.01v.01H6V6Z"/>
            </svg>
            {{ showTags ? '收起标签' : '显示标签' }}
          </button>
          <Transition name="fade">
            <div v-if="showTags" class="flex flex-wrap items-center gap-1.5">
              <span
                v-for="tag in problem.tags"
                :key="tag.id"
                class="rounded-lg bg-amber-50 px-2.5 py-1 text-xs font-medium text-amber-700 border border-amber-200 dark:bg-amber-900/20 dark:text-amber-400 dark:border-amber-800"
              >{{ tag.name }}</span>
            </div>
          </Transition>
        </div>
      </div>

      <div class="flex-1 overflow-y-auto px-6 pb-6">
        <MarkdownRenderer :content="problem.description" />

        <div v-if="sampleCases.length > 0" class="mt-8 space-y-4">
          <h3 class="text-sm font-bold text-zinc-700 dark:text-zinc-300">示例</h3>
          <div
            v-for="(tc, i) in sampleCases"
            :key="i"
            class="rounded-xl border border-zinc-200 bg-gradient-to-b from-zinc-50/80 to-zinc-50/30 p-4 shadow-sm dark:from-zinc-900/50 dark:to-zinc-900/20 dark:border-zinc-800"
          >
            <div class="mb-3">
              <div class="mb-1.5 flex items-center gap-2">
                <span class="rounded-lg bg-zinc-200 px-2 py-0.5 text-xs font-bold text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">输入 {{ i + 1 }}</span>
                <button
                  class="rounded-lg px-2 py-0.5 text-xs font-medium text-zinc-400 transition-all duration-200 hover:bg-zinc-200 hover:text-zinc-600 dark:hover:bg-zinc-800"
                  @click="copyToClipboard(tc.input, 'in-' + i)"
                >
                  {{ copiedId === 'in-' + i ? '已复制' : '复制' }}
                </button>
              </div>
              <pre class="rounded-xl border border-zinc-100 bg-white px-4 py-3 text-sm text-zinc-700 shadow-sm dark:bg-zinc-950 dark:border-zinc-800 dark:text-zinc-300">{{ tc.input }}</pre>
            </div>
            <div>
              <div class="mb-1.5 flex items-center gap-2">
                <span class="rounded-lg bg-zinc-200 px-2 py-0.5 text-xs font-bold text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">输出 {{ i + 1 }}</span>
                <button
                  class="rounded-lg px-2 py-0.5 text-xs font-medium text-zinc-400 transition-all duration-200 hover:bg-zinc-200 hover:text-zinc-600 dark:hover:bg-zinc-800"
                  @click="copyToClipboard(tc.output, 'out-' + i)"
                >
                  {{ copiedId === 'out-' + i ? '已复制' : '复制' }}
                </button>
              </div>
              <pre class="rounded-xl border border-zinc-100 bg-white px-4 py-3 text-sm text-zinc-700 shadow-sm dark:bg-zinc-950 dark:border-zinc-800 dark:text-zinc-300">{{ tc.output }}</pre>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Right: Code editor -->
    <div class="flex w-1/2 min-h-0 flex-col bg-white dark:bg-zinc-950">
      <div class="flex shrink-0 items-center justify-between border-b border-zinc-200 px-4 py-3 dark:border-zinc-800">
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
          aria-label="提交代码"
        >
          <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7"/>
          </svg>
          {{ submitting ? '提交中...' : '提交' }}
        </GlowButton>
      </div>

      <div class="flex-1 min-h-0">
        <MonacoEditor :model-value="code" :language="language" :problem-id="problemId" @update:model-value="onCodeChange" />
      </div>

      <!-- Manual debug panel -->
      <Transition name="slide-up" mode="out-in">
        <div v-if="showDebug" key="debug-panel" class="shrink-0 border-t border-zinc-200 dark:border-zinc-800">
          <div class="flex items-center justify-between bg-gradient-to-r from-zinc-50 to-zinc-100 px-4 py-2.5 dark:from-zinc-900 dark:to-zinc-900/50">
            <span class="text-xs font-bold text-zinc-500 uppercase tracking-wider">调试控制台</span>
            <div class="flex items-center gap-2">
              <button class="rounded-lg px-3 py-1 text-xs font-medium text-zinc-500 transition-all duration-200 hover:bg-zinc-200 hover:text-zinc-700 dark:hover:bg-zinc-800 dark:hover:text-zinc-300" @click="loadSampleInput" aria-label="加载示例输入">加载示例</button>
              <GlowButton :disabled="runningCode" class="px-4 py-1.5 text-xs font-semibold gap-1" @click="handleRun" aria-label="运行代码">
                <svg class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><polygon points="5 3 19 12 5 21 5 3"/></svg>
                {{ runningCode ? '运行中...' : '运行' }}
              </GlowButton>
              <button class="rounded-lg px-2 py-1 text-xs font-medium text-zinc-400 transition-colors hover:text-zinc-600 dark:hover:text-zinc-300" @click="showDebug = false" aria-label="收起调试控制台">收起</button>
            </div>
          </div>

          <div class="grid grid-cols-2 gap-3 p-4">
            <div>
              <div class="mb-1.5 text-xs font-semibold text-zinc-400 uppercase tracking-wider">输入</div>
              <textarea
                v-model="debugInput"
                rows="8"
                class="input-glow w-full resize-none rounded-xl border border-zinc-200 bg-zinc-50 px-4 py-3 text-sm font-mono text-zinc-700 placeholder:text-zinc-400 dark:bg-zinc-900 dark:text-zinc-300 dark:border-zinc-700"
                placeholder="在此粘贴测试输入..."
              ></textarea>
            </div>

            <div class="flex flex-col gap-3">
              <div>
                <div class="mb-1.5 text-xs font-semibold text-zinc-400 uppercase tracking-wider">期望输出</div>
                <textarea
                  v-model="debugExpected"
                  rows="3"
                  class="input-glow w-full resize-none rounded-xl border border-zinc-200 bg-zinc-50 px-4 py-3 text-sm font-mono text-zinc-600 placeholder:text-zinc-400 dark:bg-zinc-900 dark:text-zinc-300 dark:border-zinc-700"
                  placeholder="（选填）"
                ></textarea>
              </div>
              <div class="flex-1">
                <div class="mb-1.5 text-xs font-semibold text-zinc-400 uppercase tracking-wider">实际输出</div>
                <div
                  v-if="runningCode"
                  class="flex h-24 items-center justify-center rounded-xl border border-zinc-200 bg-zinc-50 text-sm text-zinc-400 shadow-sm dark:bg-zinc-900 dark:border-zinc-700"
                >
                  运行中...
                </div>
                <pre
                  v-else-if="runResult"
                  :class="[
                    'overflow-auto rounded-xl border p-4 text-sm font-mono shadow-sm',
                    outputMatch === true
                      ? 'border-emerald-200 bg-emerald-50/50 text-emerald-800 dark:bg-emerald-900/20 dark:border-emerald-800 dark:text-emerald-400'
                      : outputMatch === false
                        ? 'border-red-200 bg-red-50/50 text-red-800 dark:bg-red-900/20 dark:border-red-800 dark:text-red-400'
                        : runResult.status !== 'Accepted' && runResult.status !== 'Error'
                          ? 'border-red-200 bg-red-50/50 text-red-800 dark:bg-red-900/20 dark:border-red-800 dark:text-red-400'
                          : 'border-zinc-200 bg-zinc-50 text-zinc-700 dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-300'
                  ]"
                >{{ runResult.stdout || runResult.stderr || '（无输出）' }}</pre>
                <div
                  v-else
                  class="flex h-24 items-center justify-center rounded-xl border border-zinc-200 bg-zinc-50 text-sm text-zinc-400 shadow-sm dark:bg-zinc-900 dark:border-zinc-700"
                >
                  点击"运行"执行代码
                </div>
              </div>
            </div>
          </div>

          <div v-if="runResult" class="flex items-center gap-4 border-t border-zinc-200 px-4 py-2.5 dark:border-zinc-800">
            <template v-if="outputMatch !== null">
              <span
                :class="[
                  'inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs font-semibold shadow-sm',
                  outputMatch
                    ? 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400 dark:border-emerald-800'
                    : 'bg-red-50 text-red-700 border-red-200 dark:bg-red-900/30 dark:text-red-400 dark:border-red-800'
                ]"
              >
                {{ outputMatch ? '输出匹配' : '输出不匹配' }}
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
                {{ runResult.status === 'Accepted' ? '完成' : runResult.status }}
              </span>
            </template>
            <span class="text-xs text-zinc-400">时间: {{ runResult.time_used }} ms</span>
            <span class="text-xs text-zinc-400">内存: {{ runResult.memory_used }} KB</span>
          </div>
        </div>

        <div v-else key="collapsed" class="shrink-0 flex items-center justify-between border-t border-zinc-200 bg-zinc-50 px-4 py-2 dark:bg-zinc-900 dark:border-zinc-800">
          <span class="text-xs text-zinc-400">调试控制台已收起</span>
          <button class="rounded-lg bg-brand-600 px-4 py-1.5 text-xs font-semibold text-white shadow-sm shadow-brand-500/25 transition-all duration-200 hover:bg-brand-700" @click="showDebug = true">展开</button>
        </div>
      </Transition>
    </div>
  </div>

  <div v-else class="flex items-center justify-center py-32 text-sm text-zinc-400">
    加载中...
  </div>
</template>
