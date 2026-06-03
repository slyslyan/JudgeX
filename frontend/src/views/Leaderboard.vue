<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { getLeaderboard, type RankEntry } from '../api'

const router = useRouter()
const entries = ref<RankEntry[]>([])

onMounted(async () => {
  const res = await getLeaderboard()
  entries.value = res.data.leaderboard
})
</script>

<template>
  <div class="mx-auto max-w-2xl px-6 py-8">
    <div class="mb-6 flex items-center gap-3">
      <h2 class="text-xl font-bold text-zinc-900 dark:text-zinc-100">Leaderboard</h2>
      <span class="rounded-full bg-brand-100 px-2.5 py-0.5 text-xs font-semibold text-brand-700 dark:bg-brand-900/40 dark:text-brand-400">Top 50</span>
    </div>

    <div class="table-premium dark:bg-zinc-900">
      <table class="w-full">
        <thead>
          <tr class="border-b border-zinc-100 bg-gradient-to-r from-zinc-50/80 to-zinc-50/30 dark:from-zinc-800/50 dark:to-zinc-800/30 dark:border-zinc-800">
            <th class="w-20 px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400">排名</th>
            <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400">普通用户</th>
            <th class="w-24 px-4 py-3.5 text-right text-xs font-semibold uppercase tracking-wider text-zinc-400">通过数</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-zinc-100 dark:divide-zinc-800">
          <tr
            v-for="(entry, i) in entries"
            :key="entry.user_id"
            class="transition-all duration-200 hover:bg-zinc-50/50 dark:hover:bg-zinc-800/50"
          >
            <td class="px-4 py-3.5">
              <!-- Gold -->
              <span v-if="i === 0"
                class="inline-flex h-8 w-8 items-center justify-center rounded-full bg-gradient-to-br from-amber-300 to-amber-500 text-xs font-bold text-white shadow-lg shadow-amber-200 dark:shadow-amber-900/40"
              >1</span>
              <!-- Silver -->
              <span v-else-if="i === 1"
                class="inline-flex h-8 w-8 items-center justify-center rounded-full bg-gradient-to-br from-zinc-300 to-zinc-400 text-xs font-bold text-white shadow-lg shadow-zinc-200 dark:shadow-zinc-900/40"
              >2</span>
              <!-- Bronze -->
              <span v-else-if="i === 2"
                class="inline-flex h-8 w-8 items-center justify-center rounded-full bg-gradient-to-br from-amber-500 to-amber-700 text-xs font-bold text-white shadow-lg shadow-amber-200 dark:shadow-amber-900/40"
              >3</span>
              <!-- Rest -->
              <span v-else class="inline-flex h-7 w-7 items-center justify-center text-xs font-medium text-zinc-400">
                {{ i + 1 }}
              </span>
            </td>
            <td class="px-4 py-3.5">
              <span
                class="text-sm font-semibold text-zinc-800 cursor-pointer transition-colors hover:text-brand-600 dark:text-zinc-200 dark:hover:text-brand-400"
                @click="router.push(`/users/${entry.user_id}`)"
              >{{ entry.username }}</span>
            </td>
            <td class="px-4 py-3.5 text-right">
              <span
                class="inline-flex items-center gap-1 text-sm font-bold"
                :class="i === 0 ? 'text-amber-600' : i <= 2 ? 'text-brand-600' : 'text-zinc-600 dark:text-zinc-400'"
              >
                {{ entry.solved }}
                <span class="text-xs font-normal text-zinc-400">solved</span>
              </span>
            </td>
          </tr>
          <tr v-if="entries.length === 0">
            <td colspan="3" class="px-4 py-20 text-center text-sm text-zinc-400">
              暂无数据
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
