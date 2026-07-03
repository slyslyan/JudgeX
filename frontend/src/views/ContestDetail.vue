<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getContest, getContestLeaderboard } from '../api'

const route = useRoute()
const router = useRouter()
const contest = ref<any>(null)
const problems = ref<any[]>([])
const loading = ref(true)
const now = ref(Date.now())
const activeTab = ref<'problems' | 'leaderboard'>('problems')
const leaderboard = ref<any[]>([])

let timer: ReturnType<typeof setInterval> | null = null
let leaderboardES: EventSource | null = null

onMounted(async () => {
  try {
    const res = await getContest(Number(route.params.id))
    contest.value = res.data.contest
    problems.value = res.data.problems || []

    if (computeStatus() === 'Running') {
      connectLeaderboardSSE()
      // Fallback: also poll in case SSE fails
      fetchLeaderboard()
    }
  } catch {
    router.push('/contests')
  } finally {
    loading.value = false
  }

  timer = setInterval(() => {
    now.value = Date.now()
  }, 200)
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
  if (leaderboardES) leaderboardES.close()
})

function connectLeaderboardSSE() {
  const url = `/api/contests/${route.params.id}/leaderboard/events`
  const es = new EventSource(url)
  es.onmessage = (event) => {
    if (event.data === 'connected') return
    try {
      const data = JSON.parse(event.data)
      if (data.leaderboard) {
        leaderboard.value = data.leaderboard
      }
    } catch { /* ignore parse errors */ }
  }
  es.onerror = () => {
    // EventSource auto-reconnects; fallback polling handles it
  }
  leaderboardES = es
}

function computeStatus() {
  if (!contest.value) return ''
  const start = new Date(contest.value.start_time).getTime()
  const end = new Date(contest.value.end_time).getTime()
  if (now.value < start) return 'Not Started'
  if (now.value > end) return 'Ended'
  return 'Running'
}

const contestStatus = computed(() => computeStatus())

const statusConfig = (s: string) => {
  if (s === 'Running') return {
    bg: 'bg-emerald-50 dark:bg-emerald-900/30',
    text: 'text-emerald-700 dark:text-emerald-400',
    dot: 'bg-emerald-500',
  }
  if (s === 'Not Started') return {
    bg: 'bg-blue-50 dark:bg-blue-900/30',
    text: 'text-blue-700 dark:text-blue-400',
    dot: 'bg-blue-500',
  }
  return {
    bg: 'bg-zinc-100 dark:bg-zinc-800',
    text: 'text-zinc-500 dark:text-zinc-400',
    dot: 'bg-zinc-400',
  }
}
const statusText = (s: string) => {
  if (s === 'Running') return 'Running'
  if (s === 'Not Started') return 'Upcoming'
  return 'Ended'
}

function formatTime(t: string) {
  return new Date(t).toLocaleString()
}

function countdown(targetISO: string): string {
  const target = new Date(targetISO).getTime()
  const diff = target - now.value
  if (diff <= 0) return '--'
  const h = Math.floor(diff / 3600000)
  const m = Math.floor((diff % 3600000) / 60000)
  const s = Math.floor((diff % 60000) / 1000)
  return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

function handleProblemClick(problemId: number) {
  if (contestStatus.value === 'Not Started') {
    ;(window as any).$toast?.info('Contest has not started yet')
    return
  }
  if (contestStatus.value === 'Ended') {
    ;(window as any).$toast?.info('Contest has ended')
    return
  }
  router.push(`/contests/${route.params.id}/problems/${problemId}`)
}

async function fetchLeaderboard() {
  try {
    const res = await getContestLeaderboard(Number(route.params.id))
    leaderboard.value = res.data.leaderboard || []
  } catch {
    // silently ignore
  }
}

function formatPenalty(seconds: number): string {
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (h > 0) return `${h}h ${m}m`
  return `${m}m`
}
</script>

<template>
  <div class="mx-auto max-w-4xl px-6 py-8">
    <div v-if="loading" class="py-16 text-center text-sm text-zinc-400">加载中...</div>

    <template v-if="contest">
      <div class="mb-8">
        <button
          class="mb-4 inline-flex items-center gap-1 text-sm font-medium text-brand-600 transition-colors hover:text-brand-700 dark:text-brand-400"
          @click="router.push('/contests')"
        >
          &larr; 返回比赛s
        </button>

        <!-- Contest header card -->
        <div class="card-premium p-6">
          <div class="flex items-center gap-3 flex-wrap mb-3">
            <h1 class="text-2xl font-bold text-zinc-900 dark:text-zinc-100">{{ contest.title }}</h1>
            <span
              :class="[statusConfig(contestStatus).bg, statusConfig(contestStatus).text]"
              class="inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-semibold shadow-sm"
            >
              <span :class="[statusConfig(contestStatus).dot, contestStatus === 'Running' ? 'animate-pulse' : '']" class="inline-block h-1.5 w-1.5 rounded-full" />
              {{ statusText(contestStatus) }}
            </span>
            <span class="rounded-lg bg-zinc-100 px-2.5 py-1 text-xs font-medium text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">{{ contest.rule_type }}</span>
          </div>
          <p class="text-sm text-zinc-500 leading-relaxed">{{ contest.description }}</p>
          <div class="mt-4 flex gap-6 text-sm text-zinc-500 flex-wrap">
            <div class="flex items-center gap-2">
              <span class="font-semibold text-zinc-600 dark:text-zinc-400">Start:</span>
              <span class="rounded-lg bg-zinc-100 px-2.5 py-1 text-xs font-mono dark:bg-zinc-800">{{ formatTime(contest.start_time) }}</span>
            </div>
            <div class="flex items-center gap-2">
              <span class="font-semibold text-zinc-600 dark:text-zinc-400">End:</span>
              <span class="rounded-lg bg-zinc-100 px-2.5 py-1 text-xs font-mono dark:bg-zinc-800">{{ formatTime(contest.end_time) }}</span>
            </div>
          </div>

          <!-- Countdown -->
          <div v-if="contestStatus === 'Not Started'" class="mt-4 rounded-xl bg-gradient-to-r from-blue-50 to-blue-100/50 px-5 py-3.5 shadow-sm dark:from-blue-900/20 dark:to-blue-900/10">
            <span class="text-sm font-medium text-blue-700 dark:text-blue-400">距离开始: </span>
            <span class="font-mono text-lg font-bold text-blue-700 dark:text-blue-400">{{ countdown(contest.start_time) }}</span>
          </div>
          <div v-else-if="contestStatus === 'Running'" class="mt-4 rounded-xl bg-gradient-to-r from-emerald-50 to-emerald-100/50 px-5 py-3.5 shadow-sm dark:from-emerald-900/20 dark:to-emerald-900/10">
            <span class="text-sm font-medium text-emerald-700 dark:text-emerald-400">剩余时间: </span>
            <span class="font-mono text-lg font-bold text-emerald-700 dark:text-emerald-400">{{ countdown(contest.end_time) }}</span>
          </div>
        </div>
      </div>

      <!-- Tabs -->
      <div class="mb-5 flex gap-1 rounded-xl bg-zinc-100/80 p-1 backdrop-blur-sm dark:bg-zinc-800/80">
        <button
          :class="activeTab === 'problems'
            ? 'bg-white text-zinc-900 shadow-sm shadow-zinc-200/50 ring-1 ring-zinc-200/40 dark:bg-zinc-900 dark:text-zinc-100 dark:ring-zinc-700/40'
            : 'text-zinc-500 hover:text-zinc-700 dark:hover:text-zinc-300'"
          class="flex-1 rounded-lg px-4 py-2.5 text-sm font-medium transition-all duration-200"
          @click="activeTab = 'problems'"
        >
          Problems
        </button>
        <button
          :class="activeTab === 'leaderboard'
            ? 'bg-white text-zinc-900 shadow-sm shadow-zinc-200/50 ring-1 ring-zinc-200/40 dark:bg-zinc-900 dark:text-zinc-100 dark:ring-zinc-700/40'
            : 'text-zinc-500 hover:text-zinc-700 dark:hover:text-zinc-300'"
          class="flex-1 rounded-lg px-4 py-2.5 text-sm font-medium transition-all duration-200"
          @click="activeTab = 'leaderboard'"
        >
          Leaderboard
          <span v-if="leaderboard.length > 0" class="ml-1.5 rounded-full bg-brand-100 px-2 py-0.5 text-xs font-semibold text-brand-700 dark:bg-brand-900/40 dark:text-brand-400">{{ leaderboard.length }}</span>
        </button>
      </div>

      <!-- Tab content -->
      <Transition name="fade" mode="out-in">
        <div v-if="activeTab === 'problems'" key="problems">
          <div class="table-premium dark:bg-zinc-900">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-zinc-100 bg-gradient-to-r from-zinc-50/80 to-zinc-50/30 dark:from-zinc-800/50 dark:to-zinc-800/30 dark:border-zinc-800">
                  <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400 w-16">#</th>
                  <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400">标题</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="p in problems"
                  :key="p.id"
                  class="border-t border-zinc-100 transition-all duration-200 hover:bg-brand-50/30 dark:border-zinc-800 dark:hover:bg-zinc-800/50"
                  :class="contestStatus === 'Running' ? 'cursor-pointer' : 'cursor-default'"
                  @click="handleProblemClick(p.problem_id)"
                >
                  <td class="px-4 py-3.5">
                    <span class="inline-flex h-8 w-8 items-center justify-center rounded-lg bg-brand-100 text-sm font-bold text-brand-700 dark:bg-brand-900/40 dark:text-brand-400">
                      {{ p.display_id }}
                    </span>
                  </td>
                  <td class="px-4 py-3.5 font-medium text-zinc-700 transition-colors hover:text-brand-600 dark:text-zinc-300 dark:hover:text-brand-400">{{ p.problem_title || `#${p.problem_id}` }}</td>
                </tr>
              </tbody>
            </table>
            <p v-if="problems.length === 0" class="py-16 text-center text-sm text-zinc-400">
              暂无题目
            </p>
          </div>
        </div>
        <div v-else-if="activeTab === 'leaderboard'" key="leaderboard">
          <div class="table-premium dark:bg-zinc-900">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-zinc-100 bg-gradient-to-r from-zinc-50/80 to-zinc-50/30 dark:from-zinc-800/50 dark:to-zinc-800/30 dark:border-zinc-800">
                  <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400 w-16">#</th>
                  <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400">普通用户</th>
                  <th class="px-4 py-3.5 text-center text-xs font-semibold uppercase tracking-wider text-zinc-400 w-20">通过数</th>
                  <th class="px-4 py-3.5 text-right text-xs font-semibold uppercase tracking-wider text-zinc-400 w-24">Penalty</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="entry in leaderboard"
                  :key="entry.user_id"
                  class="border-t border-zinc-100 transition-all duration-200 dark:border-zinc-800"
                  :class="entry.rank <= 3 ? 'bg-gradient-to-r from-amber-50/60 to-transparent dark:from-amber-900/10 dark:to-transparent' : 'hover:bg-zinc-50/50 dark:hover:bg-zinc-800/50'"
                >
                  <td class="px-4 py-3.5">
                    <span v-if="entry.rank === 1"
                      class="inline-flex h-8 w-8 items-center justify-center rounded-full bg-gradient-to-br from-amber-300 to-amber-500 text-xs font-bold text-white shadow-lg shadow-amber-200 dark:shadow-amber-900/40"
                    >1</span>
                    <span v-else-if="entry.rank === 2"
                      class="inline-flex h-8 w-8 items-center justify-center rounded-full bg-gradient-to-br from-zinc-300 to-zinc-400 text-xs font-bold text-white shadow-lg shadow-zinc-200 dark:shadow-zinc-900/40"
                    >2</span>
                    <span v-else-if="entry.rank === 3"
                      class="inline-flex h-8 w-8 items-center justify-center rounded-full bg-gradient-to-br from-amber-500 to-amber-700 text-xs font-bold text-white shadow-lg shadow-amber-200 dark:shadow-amber-900/40"
                    >3</span>
                    <span v-else class="ml-2 text-sm text-zinc-400">{{ entry.rank }}</span>
                  </td>
                  <td class="px-4 py-3.5">
                    <span class="font-semibold text-zinc-800 cursor-pointer transition-colors hover:text-brand-600 dark:text-zinc-200 dark:hover:text-brand-400" @click="router.push(`/users/${entry.user_id}`)">
                      {{ entry.username || `User #${entry.user_id}` }}
                    </span>
                  </td>
                  <td class="px-4 py-3.5 text-center font-mono font-bold text-brand-600 dark:text-brand-400">{{ entry.solved }}</td>
                  <td class="px-4 py-3.5 text-right font-mono text-xs text-zinc-500">{{ formatPenalty(entry.penalty) }}</td>
                </tr>
              </tbody>
            </table>
            <p v-if="leaderboard.length === 0" class="py-16 text-center text-sm text-zinc-400">
              暂无提交
            </p>
          </div>
        </div>
      </Transition>
    </template>
  </div>
</template>
