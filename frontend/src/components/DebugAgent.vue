<script setup lang="ts">
import { computed } from 'vue'
import { useAiDebug, type DebugTestResult } from '../composables/useAiDebug'
import MarkdownRenderer from './MarkdownRenderer.vue'

const props = defineProps<{
  problemId: number
  code: string
  language: string
}>()

const emit = defineEmits<{
  applyFix: [code: string]
  close: []
}>()

const { state, streaming, startDebug, abort, reset } = useAiDebug()

const hasResults = computed(() => state.value.testResults && state.value.testResults.length > 0)
const hasAnalysis = computed(() => state.value.analysis.length > 0)
const hasFix = computed(() => state.value.fixedCode.length > 0)
const hasVerification = computed(() => state.value.verificationResults && state.value.verificationResults.length > 0)
const isRunning = computed(() => streaming.value)
const isIdle = computed(() => !isRunning.value && !hasResults.value && !hasAnalysis.value)

function handleStart() {
  startDebug(props.problemId, props.code, props.language)
}

function handleApplyFix() {
  emit('applyFix', state.value.fixedCode)
}

function statusColor(status: string) {
  switch (status) {
    case 'Accepted': return 'text-emerald-600 bg-emerald-50 border-emerald-200'
    case 'Wrong Answer': return 'text-red-600 bg-red-50 border-red-200'
    case 'Time Limit Exceeded': return 'text-amber-600 bg-amber-50 border-amber-200'
    case 'Runtime Error': return 'text-pink-600 bg-pink-50 border-pink-200'
    case 'Compile Error': return 'text-pink-600 bg-pink-50 border-pink-200'
    default: return 'text-zinc-500 bg-zinc-50 border-zinc-200'
  }
}

function resultIcon(passed: boolean) {
  return passed ? '✅' : '❌'
}

const steps = computed(() => {
  const phase = state.value.phase
  return [
    { key: 'loading', label: '加载题目 & 提交记录', done: phase !== '' && phase !== 'loading' },
    { key: 'testing', label: '运行测试用例', done: phase === 'analyzing' || phase === 'extracting' || phase === 'verifying' || phase === 'done', active: phase === 'testing' },
    { key: 'analyzing', label: 'AI 分析错误', done: phase === 'extracting' || phase === 'verifying' || phase === 'done', active: phase === 'analyzing' },
    { key: 'verifying', label: '验证修复代码', done: (phase === 'done' && hasVerification.value) || (phase === 'done' && !isRunning.value && hasFix.value) },
  ]
})

const progressPercent = computed(() => {
  const phase = state.value.phase
  if (phase === 'done' || phase === 'error') return 100
  if (phase === 'loading') return 10
  if (phase === 'testing') return 30
  if (phase === 'analyzing') return 60
  if (phase === 'extracting') return 80
  if (phase === 'verifying') return 90
  return 0
})
</script>

<template>
  <div class="rounded-2xl border border-zinc-200 bg-white shadow-sm overflow-hidden dark:bg-zinc-950 dark:border-zinc-800">
    <!-- Header -->
    <div class="flex items-center justify-between px-5 py-3.5 border-b border-zinc-100 dark:border-zinc-800">
      <div class="flex items-center gap-2.5">
        <div class="flex h-8 w-8 items-center justify-center rounded-xl bg-gradient-to-br from-purple-500 to-brand-600 text-white text-sm shadow-sm">
          <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m9-.75a9 9 0 1 1-18 0 9 9 0 0 1 18 0Zm-9 3.75h.008v.008H12v-.008Z" />
          </svg>
        </div>
        <span class="text-sm font-bold text-zinc-800 dark:text-zinc-200">AI Debug Agent</span>
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
        <div class="flex h-16 w-16 items-center justify-center rounded-2xl bg-gradient-to-br from-purple-50 to-brand-50 text-3xl shadow-sm mb-4">
          🐛
        </div>
        <p class="text-sm font-semibold text-zinc-700 dark:text-zinc-300">AI 调试助手</p>
        <p class="mt-1.5 text-xs text-zinc-400 text-center max-w-sm leading-relaxed">
          将使用所有隐藏测试点评测你的代码，AI 会自动分析错误原因并生成修复后的代码，然后验证修复结果。
        </p>
        <button
          class="mt-5 rounded-xl bg-gradient-to-br from-purple-600 to-brand-600 px-6 py-2.5 text-sm font-bold text-white shadow-lg shadow-purple-500/25 transition-all hover:shadow-xl hover:scale-105 active:scale-95"
          @click="handleStart"
        >
          开始调试
        </button>
      </div>

      <!-- Progress bar (during execution) -->
      <div v-if="isRunning" class="mb-5">
        <div class="flex items-center justify-between mb-2">
          <span class="text-xs font-semibold text-zinc-500">{{ state.statusMessage }}</span>
          <span class="text-xs text-zinc-400">{{ progressPercent }}%</span>
        </div>
        <div class="h-2 w-full rounded-full bg-zinc-100 overflow-hidden">
          <div
            class="h-2 rounded-full bg-gradient-to-r from-purple-500 to-brand-500 transition-all duration-500 ease-out"
            :style="{ width: progressPercent + '%' }"
          />
        </div>
      </div>

      <!-- Steps timeline -->
      <div v-if="isRunning || state.phase === 'done' || state.phase === 'error'" class="mb-5 space-y-2">
        <div
          v-for="step in steps"
          :key="step.key"
          class="flex items-center gap-2.5 text-xs"
          :class="step.done ? 'text-emerald-600' : step.active ? 'text-brand-600 font-semibold' : 'text-zinc-300'"
        >
          <span v-if="step.done" class="flex-shrink-0">✓</span>
          <span v-else-if="step.active" class="flex-shrink-0 h-2 w-2 rounded-full bg-brand-500 animate-pulse" />
          <span v-else class="flex-shrink-0 h-2 w-2 rounded-full bg-zinc-200" />
          <span>{{ step.label }}</span>
        </div>
      </div>

      <!-- Error -->
      <div
        v-if="state.error"
        class="mb-4 rounded-xl bg-red-50 border border-red-200 px-4 py-3 text-xs text-red-700 font-medium"
      >
        {{ state.error }}
      </div>

      <!-- Per-test-case results -->
      <div v-if="hasResults" class="mb-5">
        <div class="flex items-center gap-3 mb-3">
          <h3 class="text-xs font-bold text-zinc-500 uppercase tracking-wider">测试结果</h3>
          <span
            class="rounded-full border px-2.5 py-0.5 text-xs font-semibold"
            :class="state.passedCount === state.totalCount
              ? 'text-emerald-600 bg-emerald-50 border-emerald-200'
              : 'text-amber-600 bg-amber-50 border-amber-200'"
          >
            {{ state.passedCount }} / {{ state.totalCount }}
          </span>
        </div>

        <div class="space-y-2 max-h-60 overflow-y-auto">
          <div
            v-for="tr in (state.testResults as DebugTestResult[])"
            :key="tr.case_id"
            class="rounded-xl border p-3 text-xs"
            :class="tr.passed
              ? 'border-emerald-200 bg-emerald-50/30'
              : 'border-red-200 bg-red-50/30'"
          >
            <div class="flex items-center justify-between mb-1.5">
              <span class="font-bold text-zinc-700">
                {{ resultIcon(tr.passed) }} 测试点 #{{ tr.case_id }}
              </span>
              <span class="rounded-md border px-2 py-0.5 font-medium" :class="statusColor(tr.status)">
                {{ tr.status }}
              </span>
            </div>
            <div v-if="!tr.passed" class="grid grid-cols-2 gap-3 mt-2">
              <div>
                <span class="text-[10px] font-semibold text-zinc-400 uppercase">输入</span>
                <pre class="mt-0.5 rounded-lg bg-zinc-100 p-2 text-[11px] text-zinc-600 overflow-auto max-h-16">{{ tr.input }}</pre>
              </div>
              <div>
                <span class="text-[10px] font-semibold text-zinc-400 uppercase">期望输出</span>
                <pre class="mt-0.5 rounded-lg bg-zinc-100 p-2 text-[11px] text-emerald-600 overflow-auto max-h-16">{{ tr.expected }}</pre>
              </div>
              <div class="col-span-2">
                <span class="text-[10px] font-semibold text-zinc-400 uppercase">实际输出</span>
                <pre class="mt-0.5 rounded-lg bg-red-50 border border-red-200 p-2 text-[11px] text-red-600 overflow-auto max-h-16">{{ tr.actual || '(无输出)' }}</pre>
              </div>
            </div>
            <div v-if="tr.error_msg" class="mt-1.5 text-pink-600 text-[11px]">
              {{ tr.error_msg }}
            </div>
          </div>
        </div>
      </div>

      <!-- AI Analysis -->
      <div v-if="hasAnalysis" class="mb-5">
        <h3 class="text-xs font-bold text-zinc-500 uppercase tracking-wider mb-3">AI 分析报告</h3>
        <div class="rounded-xl border border-zinc-200 bg-zinc-50/50 p-4 text-sm text-zinc-700 leading-relaxed">
          <MarkdownRenderer :content="state.analysis" />
          <span
            v-if="isRunning && state.phase === 'analyzing'"
            class="ml-0.5 inline-block h-4 w-0.5 animate-pulse bg-brand-500 align-text-bottom rounded-full"
          />
        </div>
      </div>

      <!-- Fixed Code -->
      <div v-if="hasFix" class="mb-5">
        <div class="flex items-center justify-between mb-3">
          <h3 class="text-xs font-bold text-zinc-500 uppercase tracking-wider">AI 修复后的代码</h3>
          <button
            class="rounded-lg bg-gradient-to-br from-emerald-500 to-emerald-600 px-4 py-1.5 text-xs font-bold text-white shadow-sm shadow-emerald-500/20 transition-all hover:shadow-md hover:scale-105 active:scale-95"
            @click="handleApplyFix"
          >
            应用到编辑器
          </button>
        </div>
        <div class="overflow-hidden rounded-xl border border-zinc-200 bg-zinc-900 shadow-sm">
          <div class="flex items-center gap-2 border-b border-zinc-700/50 px-4 py-2">
            <span class="h-2.5 w-2.5 rounded-full bg-emerald-400/80 shadow-sm shadow-emerald-400/50" />
            <span class="h-2.5 w-2.5 rounded-full bg-amber-400/80" />
            <span class="h-2.5 w-2.5 rounded-full bg-red-400/80" />
            <span class="ml-2 text-[11px] text-zinc-500">{{ language }}</span>
          </div>
          <pre class="overflow-auto p-4 text-sm text-zinc-100 font-mono leading-relaxed"><code>{{ state.fixedCode }}</code></pre>
        </div>
      </div>

      <!-- Verification Results -->
      <div v-if="hasVerification" class="mb-2">
        <div class="flex items-center gap-3 mb-3">
          <h3 class="text-xs font-bold text-zinc-500 uppercase tracking-wider">修复验证</h3>
          <span
            class="rounded-full border px-2.5 py-0.5 text-xs font-semibold"
            :class="state.verifyPassed === state.verifyTotal
              ? 'text-emerald-600 bg-emerald-50 border-emerald-200'
              : 'text-amber-600 bg-amber-50 border-amber-200'"
          >
            {{ state.verifyPassed }} / {{ state.verifyTotal }}
          </span>
        </div>

        <div class="space-y-2 max-h-48 overflow-y-auto">
          <div
            v-for="vr in (state.verificationResults as DebugTestResult[])"
            :key="'v' + vr.case_id"
            class="rounded-xl border p-3 text-xs"
            :class="vr.passed
              ? 'border-emerald-200 bg-emerald-50/30'
              : 'border-red-200 bg-red-50/30'"
          >
            <div class="flex items-center justify-between mb-1.5">
              <span class="font-bold text-zinc-700">
                {{ resultIcon(vr.passed) }} 测试点 #{{ vr.case_id }}
              </span>
              <span class="text-zinc-400">{{ vr.time_used }} ms</span>
            </div>
            <div v-if="!vr.passed" class="grid grid-cols-2 gap-2 mt-1.5">
              <div>
                <span class="text-[10px] font-semibold text-zinc-400 uppercase">期望</span>
                <pre class="mt-0.5 rounded bg-zinc-100 p-1.5 text-[11px] text-emerald-600 overflow-auto max-h-12">{{ vr.expected }}</pre>
              </div>
              <div>
                <span class="text-[10px] font-semibold text-zinc-400 uppercase">实际</span>
                <pre class="mt-0.5 rounded bg-red-50 border border-red-200 p-1.5 text-[11px] text-red-600 overflow-auto max-h-12">{{ vr.actual || '(无输出)' }}</pre>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Final status message -->
      <div
        v-if="state.phase === 'done' && state.statusMessage"
        class="mt-3 text-xs font-semibold"
        :class="state.statusMessage.includes('✅') ? 'text-emerald-600' : state.statusMessage.includes('⚠️') ? 'text-amber-600' : 'text-zinc-500'"
      >
        {{ state.statusMessage }}
      </div>
    </div>
  </div>
</template>
