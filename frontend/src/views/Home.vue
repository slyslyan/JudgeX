<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { getProblems, getContests, getLeaderboard, getAnnouncements, type Announcement } from '../api'
import GlowButton from '../components/GlowButton.vue'

interface RankEntry {
  user_id: number
  username: string
  solved: number
}
interface Contest {
  id: number
  title: string
  description: string
  start_time: string
  end_time: string
  rule_type: string
  status: string
  created_at: string
}

const solvedCount = ref(0)
const contestCount = ref(0)
const leaderboard = ref<RankEntry[]>([])
const recentContests = ref<Contest[]>([])
const announcements = ref<Announcement[]>([])
const loadingLeaderboard = ref(true)
const loadingContests = ref(true)
const loadingAnnouncements = ref(true)

const userStr = localStorage.getItem('user')
const isLoggedIn = !!userStr

const statusTag = (s: string) => {
  if (s === 'Running') return { bg: 'bg-emerald-100 dark:bg-emerald-900/40', text: 'text-emerald-700 dark:text-emerald-400', label: '进行中' }
  if (s === 'Not Started') return { bg: 'bg-blue-100 dark:bg-blue-900/40', text: 'text-blue-700 dark:text-blue-400', label: '即将开始' }
  return { bg: 'bg-zinc-100 dark:bg-zinc-800', text: 'text-zinc-500 dark:text-zinc-400', label: '已结束' }
}

function formatTime(iso: string) {
  const d = new Date(iso)
  return d.toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' }) + ' ' + d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
}

onMounted(async () => {
  try {
    const [p, c, a] = await Promise.all([
      getProblems(1, 1),
      getContests(1, 20),
      getAnnouncements(),
    ])
    solvedCount.value = p.data.total
    contestCount.value = c.data.total
    recentContests.value = (c.data.contests as Contest[]).slice(0, 5)
    announcements.value = a.data.announcements
  } catch (e) { console.error('Failed to load home data:', e) }
  loadingContests.value = false
  loadingAnnouncements.value = false

  try {
    const lb = await getLeaderboard()
    leaderboard.value = lb.data.leaderboard.slice(0, 10)
  } catch (e) { console.error('Failed to load leaderboard:', e) }
  loadingLeaderboard.value = false
})

</script>

<template>
  <div class="min-h-[calc(100vh-3rem)]" v-icon-color>
    <!-- ==================== Hero ==================== -->
    <section class="relative overflow-hidden px-6 pt-24 pb-20 sm:pt-32 sm:pb-24">
      <div class="relative mx-auto max-w-3xl text-center">
        <h1 class="text-5xl font-bold tracking-tight text-zinc-900 sm:text-6xl lg:text-7xl dark:text-white">
          Judge<span class="text-zinc-400">X</span>
        </h1>
        <div class="mt-8 flex items-center justify-center gap-3">
          <GlowButton
            :to="isLoggedIn ? '/problems' : '/login'"
            class="gap-2 px-6 py-2.5 text-[15px] font-medium"
          >
            {{ isLoggedIn ? '开始刷题' : '免费注册' }}
            <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" d="M13 7l5 5m0 0l-5 5m5-5H6"/>
            </svg>
          </GlowButton>
          <GlowButton
            to="/contests"
            class="gap-2 px-6 py-2.5 text-[15px] font-medium"
          >
            进入比赛
          </GlowButton>
        </div>
      </div>
    </section>

    <!-- ==================== Content Grid ==================== -->
    <section class="mx-auto max-w-6xl px-6 pb-24">
      <div class="grid gap-6 lg:grid-cols-3">

        <!-- ---- Left column (2 cols) ---- -->
        <div class="flex flex-col gap-6 lg:col-span-2">

          <!-- Announcement -->
          <div class="apple-card overflow-hidden">
            <div class="flex items-center gap-2 border-b border-zinc-200/60 px-6 py-4 dark:border-zinc-800">
              <div class="flex h-8 w-8 items-center justify-center rounded-lg bg-amber-100 text-amber-600 dark:bg-amber-900/40 dark:text-amber-400">
                <svg class="h-[18px] w-[18px]" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M11 5.882V19.24a1.76 1.76 0 01-3.417.592l-2.147-6.15M18 13a3 3 0 100-6M5.436 13.683A4.001 4.001 0 017 6h1.832c4.1 0 7.625-1.234 9.168-3v14c-1.543-1.766-5.067-3-9.168-3H7a3.988 3.988 0 01-1.564-.317z"/>
                </svg>
              </div>
              <span class="text-base font-semibold text-zinc-800 dark:text-zinc-200">公告</span>
            </div>
            <div v-if="loadingAnnouncements" class="px-6 py-5 text-center text-[14px] text-zinc-400">加载中...</div>
            <div v-else-if="announcements.length === 0" class="px-6 py-5 text-center text-[14px] text-zinc-500 dark:text-zinc-500">暂无公告</div>
            <div v-else class="px-6 py-5 space-y-4">
              <div v-for="a in announcements" :key="a.id" class="flex items-start gap-4">
                <span class="mt-0.5 shrink-0 rounded bg-zinc-100 px-2.5 py-1 text-[12px] font-medium text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">{{ new Date(a.created_at).toLocaleDateString('zh-CN') }}</span>
                <div>
                  <p class="text-[15px] font-medium text-zinc-800 dark:text-zinc-200">{{ a.title }}</p>
                  <p class="mt-1 text-[14px] leading-relaxed text-zinc-500 dark:text-zinc-400 whitespace-pre-wrap">{{ a.content }}</p>
                </div>
              </div>
            </div>
          </div>

          <!-- Recent contests -->
          <div class="apple-card overflow-hidden flex flex-col">
            <div class="flex items-center justify-between border-b border-zinc-200/60 px-6 py-4 dark:border-zinc-800">
              <div class="flex items-center gap-2">
                <div class="flex h-8 w-8 items-center justify-center rounded-lg bg-accent-100 text-accent-600 dark:bg-accent-900/40 dark:text-accent-400">
                  <svg class="h-[18px] w-[18px]" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z"/>
                  </svg>
                </div>
                <span class="text-base font-semibold text-zinc-800 dark:text-zinc-200">比赛</span>
              </div>
              <router-link to="/contests" class="text-[13px] font-medium text-brand-500 hover:text-brand-600 transition-colors">查看全部 &rarr;</router-link>
            </div>
            <div v-if="loadingContests" class="flex-1 px-6 py-10 text-center text-[14px] text-zinc-400">加载中...</div>
            <div v-else-if="recentContests.length === 0" class="flex-1 px-6 py-10 text-center text-[14px] text-zinc-500 dark:text-zinc-500">暂无比赛</div>
            <div v-else class="divide-y divide-zinc-200/60 dark:divide-zinc-800">
              <router-link
                v-for="c in recentContests"
                :key="c.id"
                :to="`/contests/${c.id}`"
                class="flex items-center gap-4 px-6 py-4 transition-colors hover:bg-zinc-50 dark:hover:bg-zinc-800/40"
              >
                <span
                  class="shrink-0 rounded-md px-2.5 py-1 text-[12px] font-semibold"
                  :class="[statusTag(c.status).bg, statusTag(c.status).text]"
                >{{ statusTag(c.status).label }}</span>
                <span class="flex-1 truncate text-[15px] font-medium text-zinc-800 dark:text-zinc-200">{{ c.title }}</span>
                <span class="shrink-0 text-[13px] text-zinc-400 tabular-nums">{{ formatTime(c.start_time) }}</span>
              </router-link>
            </div>
          </div>
        </div>

        <!-- ---- Right column (1 col) ---- -->
        <div class="flex flex-col gap-6">

          <!-- Stats cards -->
          <div class="grid grid-cols-2 gap-3 max-sm:grid-cols-1">
            <div class="apple-card p-5 text-center">
              <div class="text-2xl font-bold text-brand-500 tabular-nums">{{ solvedCount }}</div>
              <div class="mt-1 text-[13px] text-zinc-400">题目总数</div>
            </div>
            <div class="apple-card p-5 text-center">
              <div class="text-2xl font-bold text-accent-500 tabular-nums">{{ contestCount }}</div>
              <div class="mt-1 text-[13px] text-zinc-400">比赛场次</div>
            </div>
          </div>

          <!-- Leaderboard -->
          <div class="apple-card overflow-hidden flex flex-col flex-1">
            <div class="flex items-center gap-2 border-b border-zinc-200/60 px-6 py-4 dark:border-zinc-800">
              <div class="flex h-8 w-8 items-center justify-center rounded-lg bg-brand-100 text-brand-600 dark:bg-brand-500/20 dark:text-brand-400">
                <svg class="h-[18px] w-[18px]" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M17 20h5v-2a3 3 0 00-5.356-1.857M17 20H7m10 0v-2c0-.656-.126-1.283-.356-1.857M7 20H2v-2a3 3 0 015.356-1.857M7 20v-2c0-.656.126-1.283.356-1.857m0 0a5.002 5.002 0 019.288 0M15 7a3 3 0 11-6 0 3 3 0 016 0z"/>
                </svg>
              </div>
              <span class="text-base font-semibold text-zinc-800 dark:text-zinc-200">排行榜 Top 10</span>
            </div>
            <div v-if="loadingLeaderboard" class="flex-1 px-6 py-10 text-center text-[14px] text-zinc-400">加载中...</div>
            <div v-else-if="leaderboard.length === 0" class="flex-1 px-6 py-10 text-center text-[14px] text-zinc-500">暂无数据</div>
            <div v-else class="divide-y divide-zinc-200/60 dark:divide-zinc-800">
              <div
                v-for="(r, i) in leaderboard"
                :key="r.user_id"
                class="flex items-center gap-3 px-6 py-3"
              >
                <span
                  v-if="i === 0" class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-amber-100 text-[12px] font-bold text-amber-600 dark:bg-amber-900/40 dark:text-amber-400"
                >1</span>
                <span
                  v-else-if="i === 1" class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-zinc-100 text-[12px] font-bold text-zinc-500 dark:bg-zinc-700 dark:text-zinc-300"
                >2</span>
                <span
                  v-else-if="i === 2" class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-amber-50 text-[12px] font-bold text-amber-700 dark:bg-amber-900/20 dark:text-amber-500"
                >3</span>
                <span
                  v-else class="w-6 text-center text-[13px] font-medium text-zinc-400 tabular-nums"
                >{{ i + 1 }}</span>
                <router-link
                  :to="`/users/${r.user_id}`"
                  class="flex-1 truncate text-[14px] font-medium text-zinc-800 hover:text-brand-500 transition-colors dark:text-zinc-200"
                >{{ r.username }}</router-link>
                <span class="shrink-0 text-[14px] font-semibold text-brand-500 tabular-nums">{{ r.solved }}</span>
              </div>
            </div>
          </div>
        </div>

      </div>
    </section>
  </div>
</template>
