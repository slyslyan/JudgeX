<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { getSubmissions, type Submission } from '../api'

const router = useRouter()
const submissions = ref<Submission[]>([])
const nextCursor = ref(0)
const hasMore = ref(false)
const loading = ref(false)

const statusFilter = ref('')
const langFilter = ref('')
const showMine = ref(false)

const statusOptions = ['Accepted', 'Wrong Answer', 'Time Limit Exceeded', 'Runtime Error', 'Compile Error']
const langOptions = ['cpp', 'python', 'java', 'go', 'rust']

function resetAndLoad() {
  submissions.value = []
  nextCursor.value = 0
  hasMore.value = false
  load()
}

async function load() {
  loading.value = true
  const filters: any = {}
  if (statusFilter.value) filters.status = statusFilter.value
  if (langFilter.value) filters.language = langFilter.value
  if (showMine.value) filters.mine = true
  const res = await getSubmissions(nextCursor.value, 20, Object.keys(filters).length > 0 ? filters : undefined)
  submissions.value = [...submissions.value, ...res.data.submissions]
  nextCursor.value = res.data.next_cursor
  hasMore.value = res.data.has_more
  loading.value = false
}

function toggleMine() {
  showMine.value = !showMine.value
  resetAndLoad()
}

watch([statusFilter, langFilter], resetAndLoad)

onMounted(load)

const statusStyle = (s: string) => {
  if (s === 'Accepted') return 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400 dark:border-emerald-800'
  if (s === 'Wrong Answer') return 'bg-red-50 text-red-700 border-red-200 dark:bg-red-900/30 dark:text-red-400 dark:border-red-800'
  if (s === 'Time Limit Exceeded') return 'bg-amber-50 text-amber-700 border-amber-200 dark:bg-amber-900/30 dark:text-amber-400 dark:border-amber-800'
  if (s === 'Runtime Error') return 'bg-pink-50 text-pink-700 border-pink-200 dark:bg-pink-900/30 dark:text-pink-400 dark:border-pink-800'
  if (s === 'Compile Error') return 'bg-pink-50 text-pink-700 border-pink-200 dark:bg-pink-900/30 dark:text-pink-400 dark:border-pink-800'
  if (s === 'pending' || s === 'judging') return 'bg-blue-50 text-blue-700 border-blue-200 dark:bg-blue-900/30 dark:text-blue-400 dark:border-blue-800'
  return 'bg-zinc-50 text-zinc-600 border-zinc-200 dark:bg-zinc-800 dark:text-zinc-400 dark:border-zinc-700'
}
</script>

<template>
  <div class="mx-auto max-w-5xl px-6 py-8">
    <h2 class="mb-6 text-xl font-bold text-zinc-900 dark:text-zinc-100">提交记录</h2>

    <!-- Filters -->
    <div class="mb-4 flex flex-wrap items-center gap-3">
      <select
        v-model="statusFilter"
        class="rounded-xl border border-zinc-200 bg-white px-4 py-2 text-sm text-zinc-600 shadow-sm transition-all duration-200 focus:border-brand-500 focus:outline-none focus:ring-2 focus:ring-brand-500/15 dark:bg-zinc-900 dark:text-zinc-300 dark:border-zinc-700"
      >
        <option value="">所有状态</option>
        <option v-for="s in statusOptions" :key="s" :value="s">{{ s }}</option>
      </select>
      <select
        v-model="langFilter"
        class="rounded-xl border border-zinc-200 bg-white px-4 py-2 text-sm text-zinc-600 shadow-sm transition-all duration-200 focus:border-brand-500 focus:outline-none focus:ring-2 focus:ring-brand-500/15 dark:bg-zinc-900 dark:text-zinc-300 dark:border-zinc-700"
      >
        <option value="">所有语言</option>
        <option v-for="l in langOptions" :key="l" :value="l">{{ l.toUpperCase() }}</option>
      </select>
      <button
        class="rounded-xl border px-4 py-2 text-sm font-medium transition-all duration-200"
        :class="showMine ? 'bg-brand-500 text-white border-brand-500' : 'bg-white text-zinc-600 border-zinc-200 hover:bg-zinc-100 dark:bg-zinc-900 dark:text-zinc-300 dark:border-zinc-700 dark:hover:bg-zinc-800'"
        @click="toggleMine"
      >
        {{ showMine ? '✓ 只看我的提交' : '只看我的提交' }}
      </button>
      <span class="ml-auto rounded-full bg-zinc-100 px-3 py-1 text-xs font-medium text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">{{ submissions.length }} 条结果</span>
    </div>

    <div class="table-premium dark:bg-zinc-900">
      <table class="w-full">
        <thead>
          <tr class="border-b border-zinc-100 bg-gradient-to-r from-zinc-50/80 to-zinc-50/30 dark:from-zinc-800/50 dark:to-zinc-800/30 dark:border-zinc-800">
            <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400">编号</th>
            <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400">题目</th>
            <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400">语言</th>
            <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400">状态</th>
            <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400">时间</th>
            <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400">内存</th>
            <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400">提交时间</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-zinc-100 dark:divide-zinc-800">
          <tr
            v-for="s in submissions"
            :key="s.id"
            class="cursor-pointer transition-all duration-200 hover:bg-brand-50/30 dark:hover:bg-zinc-800/50"
            @click="router.push(`/submissions/${s.id}`)"
          >
            <td class="px-4 py-3.5 text-sm font-mono text-zinc-400">#{{ s.id }}</td>
            <td class="px-4 py-3.5 text-sm font-medium text-zinc-700 transition-colors hover:text-brand-600 dark:text-zinc-300 dark:hover:text-brand-400">{{ s.problem_title || `#${s.problem_id}` }}</td>
            <td class="px-4 py-3.5">
              <span class="inline-flex rounded-lg bg-zinc-100 px-2 py-0.5 text-xs font-medium text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">{{ s.language }}</span>
            </td>
            <td class="px-4 py-3.5">
              <span :class="statusStyle(s.status)" class="inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs font-semibold">
                <span v-if="s.status === 'Accepted'" class="text-xs">&#10003;</span>
                <span v-else-if="s.status === 'Wrong Answer'" class="text-xs">&#10007;</span>
                {{ s.status }}
              </span>
            </td>
            <td class="px-4 py-3.5 text-sm text-zinc-500">{{ s.time_used }} ms</td>
            <td class="px-4 py-3.5 text-sm text-zinc-500">{{ s.memory_used }} KB</td>
            <td class="px-4 py-3.5 text-sm text-zinc-400">{{ new Date(s.created_at).toLocaleString() }}</td>
          </tr>
          <tr v-if="submissions.length === 0">
            <td colspan="7" class="px-4 py-20 text-center text-sm text-zinc-400">
              暂无提交
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="hasMore" class="mt-5 text-center">
      <button
        :disabled="loading"
        class="rounded-xl border border-zinc-200 bg-white px-6 py-2.5 text-sm font-medium text-zinc-600 shadow-sm transition-all duration-200 hover:bg-zinc-50 hover:shadow-md disabled:opacity-50 dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800"
        @click="load"
      >
        {{ loading ? 'Loading...' : 'Load More' }}
      </button>
    </div>
    <div v-else-if="submissions.length > 0" class="mt-5 text-center text-xs text-zinc-400">
      已加载全部提交
    </div>
  </div>
</template>
