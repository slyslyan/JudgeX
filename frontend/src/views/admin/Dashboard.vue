<script setup lang="ts">
import { ref, onMounted } from 'vue'
import axios from 'axios'
import { useSreAgent } from '../../composables/useSreAgent'

interface SystemSnapshot {
  timestamp: string
  uptime: string
  queue: { backend: string; local_buf_len: number; worker_count: number; status: string }
  submissions: { last_hour_total: number; last_hour_accepted: number; accept_rate: number; status_distribution: Record<string, number> }
  sandbox: { cgroup_path: string; cgroup_exists: boolean; status: string }
  database: { connected: boolean; status: string; max_open_conns: number; open_conns: number; in_use_conns: number; idle_conns: number }
  runtime: { goroutines: number; memory_alloc_mb: string; num_gc_cycles: number }
  recent_errors: { status: string; problem_id: number; language: string; error_sample: string; count: number }[]
  bpf: {
    enabled: boolean
    tracer_url: string
    up: number
    events_total: number
    errors_total: number
    anomaly_count: number
    edge_count: number
    top_latency: { src: string; dst: string; anomaly_score: number }[]
    mitigations: { ip: string; action: string; count: number }[]
    status: string
    error: string
    fetched_at: string
  }
}

const snapshot = ref<SystemSnapshot | null>(null)
const loading = ref(true)
const loadError = ref('')

const { state, streaming, send, abort, reset } = useSreAgent()

const message = ref('')

const quickQuestions = [
  '检查系统健康状况',
  '查看当前告警',
  '生成昨日运行报告',
  '分析评测队列状态',
  '检查错误率趋势',
]

async function loadSnapshot() {
  loading.value = true
  loadError.value = ''
  try {
    const token = localStorage.getItem('token')
    if (!token) {
      loadError.value = 'Please log in as admin to view system diagnostics.'
      loading.value = false
      return
    }
    const res = await axios.get('/api/admin/sre/snapshot', {
      timeout: 10000,
      headers: { Authorization: `Bearer ${token}` },
    })
    snapshot.value = res.data
  } catch (e: any) {
    if (e.response?.status === 401 || e.response?.status === 403) {
      loadError.value = 'Admin privileges required.'
    } else if (e.code === 'ECONNABORTED') {
      loadError.value = 'Request timed out.'
    } else {
      loadError.value = e.response?.data?.error || 'Failed to load system snapshot.'
    }
  } finally {
    loading.value = false
  }
}

async function sendMessage() {
  if (!message.value.trim()) return
  await send(message.value)
}

function handleQuickQuestion(q: string) {
  message.value = q
  send(q)
}

function handleKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    sendMessage()
  }
}

function toolIcon(name: string) {
  const icons: Record<string, string> = {
    getSystemMetrics: '📊',
    getAlerts: '🔔',
    restartJudgeNode: '🔄',
    generateReport: '📋',
    getBPFMetrics: '🌐',
  }
  return icons[name] || '🔧'
}

function toolLabel(name: string) {
  const labels: Record<string, string> = {
    getSystemMetrics: '系统指标',
    getAlerts: '告警查询',
    restartJudgeNode: '重启节点',
    generateReport: '运行报告',
    getBPFMetrics: '网络监控',
  }
  return labels[name] || name
}

const statusConfig = (s: string) => {
  if (s === 'healthy') return { bg: 'bg-emerald-50 dark:bg-emerald-900/30', text: 'text-emerald-700 dark:text-emerald-400', border: 'border-emerald-200 dark:border-emerald-800', dot: 'bg-emerald-500 shadow-emerald-400' }
  if (s === 'degraded') return { bg: 'bg-amber-50 dark:bg-amber-900/30', text: 'text-amber-700 dark:text-amber-400', border: 'border-amber-200 dark:border-amber-800', dot: 'bg-amber-500 shadow-amber-400' }
  return { bg: 'bg-red-50 dark:bg-red-900/30', text: 'text-red-700 dark:text-red-400', border: 'border-red-200 dark:border-red-800', dot: 'bg-red-500 shadow-red-400' }
}

onMounted(loadSnapshot)
</script>

<template>
  <div class="mx-auto max-w-5xl px-6 py-8">
    <div class="mb-6">
      <h2 class="text-xl font-bold text-zinc-900 dark:text-zinc-100">Admin 控制台</h2>
      <p class="mt-1 text-sm text-zinc-400">System health monitoring & AI SRE Agent</p>
      <p v-if="snapshot" class="mt-1 text-xs text-zinc-500">
        Uptime: {{ snapshot.uptime }} · {{ snapshot.runtime.goroutines }} goroutines · {{ snapshot.runtime.memory_alloc_mb }} MB
      </p>
    </div>

    <div v-if="loading" class="py-16 text-center text-sm text-zinc-400">Loading system snapshot...</div>

    <div v-else-if="snapshot">
      <!-- System Status Grid -->
      <div class="mb-6 grid grid-cols-2 gap-4">
        <div v-for="item in [
          { title: 'Judge Queue', status: snapshot.queue.status, detail: `${snapshot.queue.backend} · ${snapshot.queue.worker_count} workers`, extra: `Buffer: ${snapshot.queue.local_buf_len} tasks` },
          { title: 'Sandbox', status: snapshot.sandbox.status, detail: snapshot.sandbox.cgroup_path, extra: '' },
          { title: 'Database', status: snapshot.database.status, detail: snapshot.database.connected ? 'Connected' : 'Disconnected', extra: `Pool: ${snapshot.database.open_conns} open / ${snapshot.database.in_use_conns} in use / ${snapshot.database.idle_conns} idle` },
          { title: 'eBPF Network', status: snapshot.bpf.status, detail: `Events: ${snapshot.bpf.events_total} · Errors: ${snapshot.bpf.errors_total}`, extra: `Edges: ${snapshot.bpf.edge_count} · Anomalies: ${snapshot.bpf.anomaly_count}` },
        ]" :key="item.title" class="card-premium p-5">
          <div class="flex items-center justify-between mb-2">
            <span class="text-xs font-semibold text-zinc-400 uppercase tracking-wider">{{ item.title }}</span>
            <span
              :class="[statusConfig(item.status).bg, statusConfig(item.status).text, statusConfig(item.status).border]"
              class="inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-xs font-semibold shadow-sm"
            >
              <span :class="[statusConfig(item.status).dot, item.status === 'healthy' ? 'animate-pulse' : '']" class="inline-block h-1.5 w-1.5 rounded-full" />
              {{ item.status }}
            </span>
          </div>
          <div class="text-sm font-medium text-zinc-700 dark:text-zinc-300">{{ item.detail }}</div>
          <div v-if="item.extra" class="mt-1 text-xs text-zinc-400">{{ item.extra }}</div>
        </div>

        <!-- Submissions stats -->
        <div class="card-premium p-5">
          <div class="flex items-center justify-between mb-2">
            <span class="text-xs font-semibold text-zinc-400 uppercase tracking-wider">Submissions (1h)</span>
          </div>
          <div class="text-2xl font-bold text-zinc-800 dark:text-zinc-200">{{ snapshot.submissions.last_hour_total }}</div>
          <div class="mt-1 flex items-center gap-2 text-xs">
            <span class="rounded-full bg-emerald-100 px-2 py-0.5 font-semibold text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400">{{ snapshot.submissions.accept_rate.toFixed(1) }}% accept</span>
            <span class="text-zinc-400">{{ snapshot.submissions.last_hour_accepted }} accepted</span>
          </div>
        </div>
      </div>

      <!-- Status Distribution -->
      <div v-if="Object.keys(snapshot.submissions.status_distribution).length" class="card-premium mb-6 p-5">
        <h3 class="mb-3 text-sm font-bold text-zinc-700 dark:text-zinc-300">Status Distribution (Last Hour)</h3>
        <div class="flex flex-wrap gap-2">
          <span v-for="(count, status) in snapshot.submissions.status_distribution" :key="status"
            class="inline-flex items-center gap-2 rounded-xl border border-zinc-200 bg-gradient-to-r from-zinc-50 to-zinc-50/50 px-3 py-1.5 text-xs shadow-sm dark:from-zinc-900/50 dark:to-zinc-900/20 dark:border-zinc-800">
            <span class="font-semibold text-zinc-600 dark:text-zinc-400">{{ status }}</span>
            <span class="rounded-full bg-brand-100 px-2 py-0.5 font-bold text-brand-700 dark:bg-brand-900/40 dark:text-brand-400">{{ count }}</span>
          </span>
        </div>
      </div>

      <!-- Recent Errors -->
      <div v-if="snapshot.recent_errors && snapshot.recent_errors.length" class="card-premium mb-6 p-5">
        <h3 class="mb-3 text-sm font-bold text-zinc-700 dark:text-zinc-300">Recent Error Patterns</h3>
        <div class="space-y-2">
          <div v-for="(e, i) in snapshot.recent_errors.slice(0, 5)" :key="i"
            class="flex items-start gap-3 rounded-xl bg-gradient-to-r from-red-50/50 to-transparent px-4 py-3 text-xs dark:from-red-900/10 dark:to-transparent">
            <span class="rounded-lg bg-red-100 px-2 py-1 font-bold text-red-700 dark:bg-red-900/30 dark:text-red-400">{{ e.status }}</span>
            <span class="text-zinc-500">Problem #{{ e.problem_id }} · {{ e.language }} · <span class="font-bold text-zinc-700 dark:text-zinc-300">×{{ e.count }}</span></span>
            <span class="flex-1 truncate text-zinc-400 italic">{{ e.error_sample || '(no message)' }}</span>
          </div>
        </div>
      </div>

      <!-- SRE Agent -->
      <div class="card-premium p-5" style="border-left: 3px solid #7c3aed;">
        <div class="flex items-center gap-2.5 mb-1">
          <div class="flex h-7 w-7 items-center justify-center rounded-lg bg-gradient-to-br from-purple-500 to-brand-600 text-white text-xs shadow-sm">
            <svg class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" d="M9.75 3.104v5.714a2.25 2.25 0 0 1-.659 1.591L5 14.5M9.75 3.104c-.251.023-.501.05-.75.082m.75-.082a24.301 24.301 0 0 1 4.5 0m0 0v5.714c0 .597.237 1.17.659 1.591L19.8 15.3M14.25 3.104c.251.023.501.05.75.082M19.8 15.3l-1.57.393A9.065 9.065 0 0 1 12 15a9.065 9.065 0 0 0-6.23.693L5 14.5m14.8.8 1.402 1.402c1.232 1.232.65 3.318-1.067 3.611A48.309 48.309 0 0 1 12 21c-2.773 0-5.491-.235-8.135-.687-1.718-.293-2.3-2.379-1.067-3.61L5 14.5" />
            </svg>
          </div>
          <h3 class="text-sm font-bold text-purple-700 dark:text-purple-400">SRE Ops Agent</h3>
        </div>
        <p class="mb-4 text-xs text-zinc-400">
          运维 Agent 可以获取系统指标、查询告警、重启评测节点、生成运行报告。
        </p>

        <!-- Status timeline -->
        <div v-if="state.phase !== 'idle'" class="mb-3 flex flex-wrap items-center gap-2 text-xs">
          <span
            v-if="state.phase === 'executing'"
            class="inline-flex items-center gap-1.5 rounded-full bg-purple-50 border border-purple-200 px-3 py-1 text-purple-700"
          >
            <span class="h-1.5 w-1.5 animate-pulse rounded-full bg-purple-500" />
            {{ state.statusMessage || '正在执行...' }}
          </span>
          <span
            v-else-if="state.phase === 'analyzing'"
            class="inline-flex items-center gap-1.5 rounded-full bg-blue-50 border border-blue-200 px-3 py-1 text-blue-700"
          >
            <span class="h-1.5 w-1.5 animate-pulse rounded-full bg-blue-500" />
            AI 正在分析...
          </span>
          <span
            v-else-if="state.phase === 'done'"
            class="inline-flex items-center gap-1.5 rounded-full bg-emerald-50 border border-emerald-200 px-3 py-1 text-emerald-700"
          >
            完成
          </span>
        </div>

        <!-- Tool results -->
        <div v-if="state.toolResults.length" class="mb-4 space-y-2">
          <div
            v-for="(tr, i) in state.toolResults"
            :key="i"
            class="rounded-xl border p-3 text-xs"
            :class="tr.success ? 'border-zinc-200 bg-zinc-50/50' : 'border-red-200 bg-red-50'"
          >
            <div class="flex items-center justify-between mb-1">
              <span class="font-semibold text-zinc-700">
                {{ toolIcon(tr.tool) }} {{ toolLabel(tr.tool) }}
              </span>
              <span
                class="rounded-full border px-2 py-0.5 font-medium"
                :class="tr.success ? 'text-emerald-600 bg-emerald-50 border-emerald-200' : 'text-red-600 bg-red-50 border-red-200'"
              >
                {{ tr.success ? '成功' : '失败' }}
              </span>
            </div>
            <div v-if="tr.error" class="text-red-600 mt-1">{{ tr.error }}</div>
          </div>
        </div>

        <!-- Agent response -->
        <div v-if="state.response" class="mb-4 rounded-xl border border-zinc-200 bg-white p-4 shadow-sm dark:bg-zinc-900 dark:border-zinc-700">
          <div class="prose prose-sm max-w-none text-zinc-700 whitespace-pre-wrap dark:text-zinc-300">{{ state.response }}</div>
          <span v-if="streaming && state.phase === 'analyzing'" class="ml-0.5 inline-block h-4 w-0.5 animate-pulse bg-brand-500 align-text-bottom">&nbsp;</span>
        </div>

        <!-- Error -->
        <p v-if="state.error" class="mb-3 text-sm text-red-500">{{ state.error }}</p>

        <!-- Input area -->
        <div class="flex items-end gap-2 mb-3">
          <textarea
            v-model="message"
            :disabled="streaming"
            rows="1"
            class="input-glow max-h-24 min-h-[38px] flex-1 resize-none rounded-xl border border-zinc-200 bg-white px-4 py-2.5 text-sm dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200"
            placeholder="输入问题，例如：检查系统健康状况、生成昨日报告..."
            @keydown="handleKeydown"
          ></textarea>
          <button
            v-if="streaming"
            class="rounded-xl bg-red-500 px-4 py-2.5 text-sm font-medium text-white shadow-sm shadow-red-500/25 hover:bg-red-600"
            @click="abort"
          >Stop</button>
          <button
            v-else
            class="rounded-xl bg-gradient-to-br from-purple-600 to-brand-600 px-5 py-2.5 text-sm font-bold text-white shadow-lg shadow-purple-500/20 transition-all hover:shadow-xl hover:scale-105 active:scale-95 disabled:opacity-50"
            :disabled="!message.trim() || streaming"
            @click="sendMessage"
          >发送</button>
        </div>

        <!-- Quick questions -->
        <div class="flex flex-wrap gap-1.5">
          <button
            v-for="q in quickQuestions"
            :key="q"
            :disabled="streaming"
            class="rounded-xl border border-zinc-200 bg-white px-3 py-1.5 text-xs font-medium text-zinc-500 shadow-sm transition-all duration-200 hover:bg-zinc-50 hover:shadow hover:border-purple-300 hover:text-purple-600 disabled:opacity-50 dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800"
            @click="handleQuickQuestion(q)"
          >{{ q }}</button>
          <button
            v-if="state.phase !== 'idle'"
            class="rounded-xl border border-zinc-200 bg-white px-3 py-1.5 text-xs font-medium text-zinc-400 hover:text-zinc-600"
            @click="reset"
          >清除</button>
        </div>
      </div>
    </div>

    <div v-else class="py-16 text-center">
      <p class="text-sm text-red-500 mb-4">{{ loadError }}</p>
      <button class="rounded-xl border border-zinc-200 bg-white px-4 py-2 text-xs font-medium text-zinc-600 shadow-sm transition-all duration-200 hover:bg-zinc-50 dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800" @click="loadSnapshot">Retry</button>
    </div>
  </div>
</template>
