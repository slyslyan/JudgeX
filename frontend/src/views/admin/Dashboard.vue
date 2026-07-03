<script setup lang="ts">
import { ref, onMounted } from 'vue'
import axios from 'axios'
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

const statusConfig = (s: string) => {
  if (s === 'healthy') return { bg: 'bg-emerald-50 dark:bg-emerald-900/30', text: 'text-emerald-700 dark:text-emerald-400', border: 'border-emerald-200 dark:border-emerald-800', dot: 'bg-emerald-500 shadow-emerald-400' }
  if (s === 'degraded') return { bg: 'bg-amber-50 dark:bg-amber-900/30', text: 'text-amber-700 dark:text-amber-400', border: 'border-amber-200 dark:border-amber-800', dot: 'bg-amber-500 shadow-amber-400' }
  return { bg: 'bg-red-50 dark:bg-red-900/30', text: 'text-red-700 dark:text-red-400', border: 'border-red-200 dark:border-red-800', dot: 'bg-red-500 shadow-red-400' }
}

onMounted(loadSnapshot)
</script>

<template>
  <div class="mx-auto max-w-5xl px-6 py-8" v-icon-color>
    <div class="mb-6">
      <h2 class="text-xl font-bold text-zinc-900 dark:text-zinc-100">Admin 控制台</h2>
      <p class="mt-1 text-sm text-zinc-400">系统健康监控</p>
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

    </div>

    <div v-else class="py-16 text-center">
      <p class="text-sm text-red-500 mb-4">{{ loadError }}</p>
      <button class="rounded-xl border border-zinc-200 bg-white px-4 py-2 text-xs font-medium text-zinc-600 shadow-sm transition-all duration-200 hover:bg-zinc-50 dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800" @click="loadSnapshot">Retry</button>
    </div>
  </div>
</template>
