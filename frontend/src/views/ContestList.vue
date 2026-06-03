<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { getContests } from '../api'

const router = useRouter()

interface Contest {
  id: number
  title: string
  description: string
  start_time: string
  end_time: string
  rule_type: string
  status: string
}

const contests = ref<Contest[]>([])

const userStr = localStorage.getItem('user')
const user = userStr ? JSON.parse(userStr) : null
const isAdmin = computed(() => user?.role === 'admin' || user?.role === 'super_admin')

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

function countdown(start: string, end: string, status: string) {
  const now = Date.now()
  const target = status === 'Not Started' ? new Date(start) : new Date(end)
  const diff = target.getTime() - now
  if (diff <= 0) return ''
  const h = Math.floor(diff / 3600000)
  const m = Math.floor((diff % 3600000) / 60000)
  const s = Math.floor((diff % 60000) / 1000)
  const prefix = status === 'Not Started' ? 'Starts in ' : 'Ends in '
  return prefix + `${h}h ${m}m ${s}s`
}

onMounted(async () => {
  const res = await getContests()
  contests.value = res.data.contests
})
</script>

<template>
  <div class="mx-auto max-w-4xl px-6 py-8">
    <div class="mb-6 flex items-center justify-between">
      <h2 class="text-xl font-bold text-zinc-900 dark:text-zinc-100">Contests</h2>
      <router-link
        v-if="isAdmin"
        to="/admin/contests/new"
        class="btn-gradient text-xs"
      >
        + New Contest
      </router-link>
    </div>

    <div class="space-y-4">
      <div
        v-for="c in contests"
        :key="c.id"
        class="card-premium group cursor-pointer p-6"
        @click="router.push(`/contests/${c.id}`)"
      >
        <div class="flex items-start justify-between">
          <div class="flex-1">
            <div class="flex items-center gap-3 mb-2">
              <h3 class="text-lg font-bold text-zinc-900 transition-colors group-hover:text-brand-600 dark:text-zinc-100 dark:group-hover:text-brand-400">{{ c.title }}</h3>
              <span
                :class="[statusConfig(c.status).bg, statusConfig(c.status).text]"
                class="inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-semibold shadow-sm"
              >
                <span :class="statusConfig(c.status).dot" class="inline-block h-1.5 w-1.5 rounded-full animate-pulse" />
                {{ statusText(c.status) }}
              </span>
              <span class="rounded-lg bg-zinc-100 px-2.5 py-1 text-xs font-medium text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">{{ c.rule_type }}</span>
            </div>
            <p class="text-sm text-zinc-500 line-clamp-2 leading-relaxed">{{ c.description }}</p>
          </div>
        </div>
        <div class="mt-4 flex items-center gap-4 text-xs text-zinc-400">
          <span class="inline-flex items-center gap-1.5">
            <svg class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M8 7V3m8 4V3m-9 8h10M5 21h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z"/></svg>
            {{ new Date(c.start_time).toLocaleString() }}
          </span>
          <span>&rarr;</span>
          <span class="inline-flex items-center gap-1.5">
            <svg class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M8 7V3m8 4V3m-9 8h10M5 21h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z"/></svg>
            {{ new Date(c.end_time).toLocaleString() }}
          </span>
          <span
            v-if="countdown(c.start_time, c.end_time, c.status)"
            class="ml-auto rounded-lg px-3 py-1.5 text-xs font-semibold font-mono"
            :class="c.status === 'Running'
              ? 'bg-emerald-50 text-emerald-700 shadow-sm shadow-emerald-200/50 dark:bg-emerald-900/30 dark:text-emerald-400'
              : 'bg-blue-50 text-blue-700 shadow-sm shadow-blue-200/50 dark:bg-blue-900/30 dark:text-blue-400'"
          >
            {{ countdown(c.start_time, c.end_time, c.status) }}
          </span>
        </div>
      </div>

      <p v-if="contests.length === 0" class="py-20 text-center text-sm text-zinc-400">
        暂无比赛
      </p>
    </div>
  </div>
</template>
