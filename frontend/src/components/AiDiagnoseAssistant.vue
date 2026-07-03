<script setup lang="ts">
import { ref, computed } from 'vue'
import { useAiDiagnose, type AiDiagnoseOptions } from '../composables/useAiDiagnose'
import MarkdownRenderer from './MarkdownRenderer.vue'

const props = defineProps<{
  problemId: number
  code: string
  language: string
  verdict: string
  compileError?: string
  timeUsed?: number
}>()

const emit = defineEmits<{
  close: []
}>()

const { state, streaming, start, abort, reset } = useAiDiagnose()
const showDetails = ref(true)

const verdictLabel = computed(() => {
  const labels: Record<string, string> = {
    CE: 'Compile Error',
    TLE: 'Time Limit Exceeded',
    WA: 'Wrong Answer',
    RE: 'Runtime Error',
  }
  return labels[props.verdict] || props.verdict
})

const verdictColor = computed(() => {
  const colors: Record<string, string> = {
    CE: 'bg-pink-50 text-pink-700 border-pink-200 dark:bg-pink-900/30 dark:text-pink-400 dark:border-pink-800',
    TLE: 'bg-amber-50 text-amber-700 border-amber-200 dark:bg-amber-900/30 dark:text-amber-400 dark:border-amber-800',
    WA: 'bg-red-50 text-red-700 border-red-200 dark:bg-red-900/30 dark:text-red-400 dark:border-red-800',
    RE: 'bg-pink-50 text-pink-700 border-pink-200 dark:bg-pink-900/30 dark:text-pink-400 dark:border-pink-800',
  }
  return colors[props.verdict] || 'bg-zinc-50 text-zinc-600 border-zinc-200'
})

const modeLabel = computed(() => {
  if (props.verdict === 'CE') return '静态分析（编译错误）'
  if (props.verdict === 'TLE') return '静态分析（时间复杂度）'
  return '动态分析（插桩 + 执行轨迹）'
})

const isIdle = computed(() => state.value.phase === 'idle')
const isRunning = computed(() => streaming.value)
const isDone = computed(() => state.value.phase === 'done')
const isError = computed(() => state.value.phase === 'error')
const hasAnalysis = computed(() => state.value.analysis.length > 0)
const hasTrace = computed(() => state.value.traceOutput.length > 0)

function handleStart() {
  const opts: AiDiagnoseOptions = {
    problemId: props.problemId,
    language: props.language,
    code: props.code,
    verdict: props.verdict,
    compileError: props.compileError,
    timeUsed: props.timeUsed,
  }
  start(opts)
}
</script>

<template>
  <div class="rounded-2xl border border-zinc-200 bg-white shadow-sm overflow-hidden dark:bg-zinc-950 dark:border-zinc-800">
    <!-- Header -->
    <div class="flex items-center justify-between px-5 py-3.5 border-b border-zinc-100 dark:border-zinc-800">
      <div class="flex items-center gap-2.5">
        <div class="flex h-8 w-8 items-center justify-center rounded-xl bg-gradient-to-br from-amber-500 to-orange-600 text-white text-sm shadow-sm">
          <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126ZM12 15.75h.007v.008H12v-.008Z" />
          </svg>
        </div>
        <span class="text-sm font-bold text-zinc-800 dark:text-zinc-200">AI 诊断助手</span>
        <span class="rounded-full border px-2 py-0.5 text-[10px] font-semibold" :class="verdictColor">
          {{ verdictLabel }}
        </span>
      </div>
      <div class="flex items-center gap-2">
        <button
          v-if="isRunning"
          class="rounded-lg bg-red-50 border border-red-200 px-3 py-1.5 text-xs font-semibold text-red-600 transition-colors hover:bg-red-100"
          @click="abort"
        >
          停止
        </button>
        <button
          v-if="!isIdle && !isRunning"
          class="rounded-lg bg-zinc-100 border border-zinc-200 px-3 py-1.5 text-xs font-medium text-zinc-500 transition-colors hover:bg-zinc-200"
          @click="reset"
        >
          重置
        </button>
        <button
          class="rounded-lg px-2 py-1.5 text-xs font-medium text-zinc-400 transition-colors hover:bg-zinc-100 hover:text-zinc-600 dark:hover:bg-zinc-800"
          @click="emit('close')"
        >
          关闭
        </button>
      </div>
    </div>

    <div class="p-5">
      <!-- Idle state -->
      <div v-if="isIdle" class="flex flex-col items-center py-8">
        <div class="flex h-16 w-16 items-center justify-center rounded-2xl bg-gradient-to-br from-amber-50 to-orange-50 text-3xl shadow-sm mb-4">
          <svg class="h-8 w-8 text-amber-500" fill="none" stroke="currentColor" stroke-width="1.5" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" d="M4.26 10.147a60.438 60.438 0 0 0-.491 6.347A48.62 48.62 0 0 1 12 20.904a48.62 48.62 0 0 1 8.232-4.41 60.46 60.46 0 0 0-.491-6.347m-15.482 0a50.636 50.636 0 0 0-2.658-.813A59.906 59.906 0 0 1 12 3.493a59.903 59.903 0 0 1 10.399 5.84c-.896.248-1.783.52-2.658.814m-15.482 0A50.717 50.717 0 0 1 12 13.489a50.702 50.702 0 0 1 7.74-3.342" />
          </svg>
        </div>
        <p class="text-sm font-semibold text-zinc-700 dark:text-zinc-300">AI 诊断助手</p>
        <p class="mt-1.5 text-xs text-zinc-400 text-center max-w-sm leading-relaxed">
          基于 {{ verdictLabel }} 结果，使用 {{ modeLabel }} 方式分析代码，
          通过苏格拉底式提问引导你发现问题根因。
        </p>
        <button
          class="mt-5 rounded-xl bg-gradient-to-br from-amber-600 to-orange-600 px-6 py-2.5 text-sm font-bold text-white shadow-lg shadow-amber-500/25 transition-all hover:shadow-xl hover:scale-105 active:scale-95"
          @click="handleStart"
          :disabled="isRunning"
        >
          开始诊断
        </button>
      </div>

      <!-- Running state -->
      <div v-if="isRunning" class="mb-4">
        <div class="flex items-center justify-between mb-2">
          <span class="text-xs font-semibold text-zinc-500">{{ state.status || '诊断中...' }}</span>
        </div>
        <div class="h-1.5 w-full rounded-full bg-zinc-100 overflow-hidden">
          <div class="h-1.5 rounded-full bg-gradient-to-r from-amber-400 to-orange-500 animate-pulse transition-all" style="width: 60%" />
        </div>
      </div>

      <!-- Error -->
      <div
        v-if="isError && state.error"
        class="mb-4 rounded-xl bg-red-50 border border-red-200 px-4 py-3 text-xs text-red-700 font-medium"
      >
        {{ state.error }}
      </div>

      <!-- Trace output (collapsible) -->
      <div v-if="hasTrace" class="mb-4">
        <button
          class="flex items-center gap-2 text-xs font-semibold text-zinc-500 uppercase tracking-wider mb-2"
          @click="showDetails = !showDetails"
        >
          <svg class="h-3 w-3 transition-transform" :class="showDetails ? 'rotate-90' : ''" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7" />
          </svg>
          执行轨迹
        </button>
        <pre v-if="showDetails" class="rounded-xl bg-zinc-900 p-4 text-xs text-zinc-300 font-mono overflow-auto max-h-80 leading-relaxed">{{ state.traceOutput }}</pre>
      </div>

      <!-- AI Analysis -->
      <div
        v-if="hasAnalysis"
        ref="analysisRef"
        class="rounded-xl border-2 p-4 transition-all duration-500"
        :class="isDone ? 'border-amber-200 bg-amber-50/30 dark:bg-amber-900/10 dark:border-amber-800' : 'border-transparent'"
      >
        <div class="flex items-center justify-between mb-3">
          <h3 class="text-xs font-bold text-zinc-500 uppercase tracking-wider">AI 分析报告</h3>
          <span v-if="isDone" class="rounded-full bg-amber-100 px-2.5 py-0.5 text-[10px] font-semibold text-amber-700 dark:bg-amber-900/30 dark:text-amber-400">
            AI 诊断
          </span>
        </div>
        <div class="rounded-xl border border-zinc-200 bg-zinc-50/50 p-4 text-sm text-zinc-700 leading-relaxed dark:bg-zinc-900/20 dark:border-zinc-700 dark:text-zinc-300">
          <MarkdownRenderer :content="state.analysis" />
          <span
            v-if="isRunning"
            class="ml-0.5 inline-block h-4 w-0.5 animate-pulse bg-amber-500 align-text-bottom rounded-full"
          />
        </div>
      </div>

      <!-- Final status -->
      <div
        v-if="isDone && state.status"
        class="mt-3 text-xs font-semibold text-zinc-500"
      >
        {{ state.status }}
      </div>

      <!-- Retry on error -->
      <div v-if="isError" class="mt-4 text-center">
        <button
          class="rounded-xl bg-zinc-100 border border-zinc-200 px-5 py-2 text-xs font-semibold text-zinc-600 transition-colors hover:bg-zinc-200"
          @click="handleStart"
        >
          重试
        </button>
      </div>
    </div>
  </div>
</template>
