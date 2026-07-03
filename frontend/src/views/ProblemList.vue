<script setup lang="ts">
import { ref, onMounted, watch, computed } from 'vue'
import { useRouter } from 'vue-router'
import { getProblems, deleteProblem, type Problem, type ProblemTag } from '../api'
import PaginationWhite from '../components/PaginationWhite.vue'

const router = useRouter()
const problems = ref<Problem[]>([])
const loading = ref(true)
const total = ref(0)
const page = ref(1)
const pageSize = 20
const totalPages = ref(1)
const search = ref('')
const selectedTag = ref('')
const allTags = ref<ProblemTag[]>([])
const showTagDropdown = ref(false)
const tagSearch = ref('')
const tagDropdownRef = ref<HTMLElement | null>(null)
let debounceTimer: ReturnType<typeof setTimeout> | null = null

const filteredTags = computed(() => {
  if (!tagSearch.value) return allTags.value
  return allTags.value.filter(t => t.name.toLowerCase().includes(tagSearch.value.toLowerCase()))
})

function toggleTagDropdown() {
  showTagDropdown.value = !showTagDropdown.value
  if (showTagDropdown.value) tagSearch.value = ''
}

function selectTag(tag: string) {
  selectedTag.value = selectedTag.value === tag ? '' : tag
  showTagDropdown.value = false
  page.value = 1
  load()
}

function onDocumentClick(e: MouseEvent) {
  if (tagDropdownRef.value && !tagDropdownRef.value.contains(e.target as Node)) {
    showTagDropdown.value = false
  }
}

onMounted(() => {
  load()
  document.addEventListener('click', onDocumentClick)
})

watch(page, load)

function readUser() {
  const s = localStorage.getItem('user')
  return s ? JSON.parse(s) : null
}
const isAdmin = computed(() => {
  const role = readUser()?.role
  return role === 'admin' || role === 'super_admin'
})

async function load() {
  loading.value = true
  try {
    const res = await getProblems(page.value, pageSize, search.value, selectedTag.value)
    problems.value = res.data.problems
    total.value = res.data.total
    totalPages.value = Math.ceil(total.value / pageSize)
    if (res.data.tags) allTags.value = res.data.tags
  } catch (e) {
    console.error('Failed to load problems:', e)
  } finally {
    loading.value = false
  }
}

function goTo(id: number) {
  router.push(`/problems/${id}`)
}

async function remove(id: number) {
  if (!confirm('确定要删除这道题目吗？')) return
  await deleteProblem(id)
  load()
}


function onSearchInput() {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => {
    page.value = 1
    load()
  }, 300)
}

</script>

<template>
  <div class="mx-auto max-w-4xl px-6 py-8" v-icon-color>
    <div class="mb-6 flex items-center justify-between gap-3">
      <h2 class="text-xl font-semibold tracking-tight text-zinc-900 dark:text-white">题库</h2>
      <div class="flex items-center gap-3">
        <div class="relative">
          <svg class="absolute left-3.5 top-1/2 h-4 w-4 -translate-y-1/2 text-zinc-400" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"/>
          </svg>
          <input
            v-model="search"
            type="text"
            placeholder="搜索题目..."
            class="w-56 rounded-full border border-zinc-200 bg-zinc-50 py-2 pl-9 pr-4 text-sm text-zinc-700 placeholder:text-zinc-400 focus:border-brand-500 focus:outline-none focus:ring-2 focus:ring-brand-500/10 dark:bg-zinc-800 dark:border-zinc-700 dark:text-zinc-300"
            @input="onSearchInput"
          />
        </div>
        <router-link
          v-if="isAdmin"
          to="/admin/problems/new"
          class="apple-btn-primary"
        >
          + 新建题目
        </router-link>
      </div>
    </div>

    <div class="mb-4 flex items-center gap-2">
      <div ref="tagDropdownRef" class="relative">
        <button
          class="flex items-center gap-1.5 rounded-full px-3 py-1.5 text-xs font-medium transition-all"
          :class="selectedTag
            ? 'bg-zinc-900 text-white dark:bg-white dark:text-black'
            : 'bg-zinc-100 text-zinc-600 hover:bg-zinc-200 dark:bg-zinc-800 dark:text-zinc-400 dark:hover:bg-zinc-700'"
          @click.stop="toggleTagDropdown"
        >
          <svg class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" d="M7 7h.01M7 3h5c.512 0 1.024.195 1.414.586l7 7a2 2 0 010 2.828l-7 7a2 2 0 01-2.828 0l-7-7A1.994 1.994 0 013 12V7a4 4 0 014-4z"/>
          </svg>
          {{ selectedTag || '选择标签' }}
          <svg v-if="selectedTag" class="ml-0.5 h-3 w-3 cursor-pointer opacity-60 hover:opacity-100" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24" @click.stop="selectTag('')">
            <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12"/>
          </svg>
        </button>

        <Transition name="fade">
          <div v-if="showTagDropdown" class="absolute left-0 top-full mt-1.5 z-20 w-56 rounded-2xl border border-zinc-200 bg-white p-3 shadow-xl dark:border-zinc-700 dark:bg-zinc-900">
            <input
              v-model="tagSearch"
              type="text"
              placeholder="搜索标签..."
              class="mb-2 w-full rounded-xl border border-zinc-200 bg-zinc-50 px-3 py-1.5 text-xs dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-200"
              @click.stop
            />
            <div class="max-h-48 overflow-y-auto">
              <button
                v-for="tag in filteredTags"
                :key="tag.id"
                class="block w-full rounded-lg px-3 py-1.5 text-left text-xs font-medium transition-colors"
                :class="selectedTag === tag.name
                  ? 'bg-zinc-900 text-white dark:bg-white dark:text-black'
                  : 'text-zinc-600 hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800'"
                @click="selectTag(tag.name)"
              >{{ tag.name }}</button>
              <div v-if="filteredTags.length === 0" class="py-4 text-center text-xs text-zinc-400">无匹配标签</div>
            </div>
          </div>
        </Transition>
      </div>
    </div>

    <div v-if="loading" class="flex items-center justify-center py-20 text-sm text-zinc-400">
      <svg class="mr-2 h-4 w-4 animate-spin" fill="none" viewBox="0 0 24 24"><circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"/><path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"/></svg>
      加载中...
    </div>

    <div v-else class="apple-table">
      <table class="w-full">
        <thead>
          <tr>
            <th>#</th>
            <th>标题</th>
            <th class="text-center w-16">通过</th>
            <th class="text-center w-16">提交</th>
            <th v-if="isAdmin" class="text-right w-32">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="p in problems"
            :key="p.id"
            class="cursor-pointer"
            @click="goTo(p.id)"
          >
            <td class="text-sm text-zinc-400 font-mono">{{ p.number || p.id }}</td>
            <td>
              <div class="flex items-center gap-2">
                <span class="text-[15px] text-zinc-800 dark:text-zinc-200">{{ p.title }}</span>
                <span
                  v-for="tag in p.tags"
                  :key="tag.id"
                  class="rounded-full bg-zinc-100 px-2 py-0.5 text-[11px] font-medium text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400"
                >{{ tag.name }}</span>
              </div>
            </td>
            <td class="text-center text-sm font-semibold text-accent-500">{{ p.accepted_count }}</td>
            <td class="text-center text-sm text-zinc-400">{{ p.submission_count }}</td>
            <td v-if="isAdmin" class="text-right">
              <div class="flex items-center justify-end gap-1">
                <button class="rounded-lg px-2.5 py-1.5 text-xs font-medium text-zinc-500 transition-colors hover:bg-zinc-100 dark:hover:bg-zinc-800" @click.stop="router.push(`/admin/problems/${p.id}/edit`)">编辑</button>
                <button class="rounded-lg px-2.5 py-1.5 text-xs font-medium text-zinc-500 transition-colors hover:bg-zinc-100 dark:hover:bg-zinc-800" @click.stop="router.push(`/admin/problems/${p.id}/testcases`)">测试</button>
                <button class="rounded-lg px-2.5 py-1.5 text-xs font-medium text-red-400 transition-colors hover:bg-red-50 dark:hover:bg-red-900/30" @click.stop="remove(p.id)">删除</button>
              </div>
            </td>
          </tr>
          <tr v-if="problems.length === 0">
            <td :colspan="isAdmin ? 5 : 4" class="py-20 text-center text-sm text-zinc-400">
              没有找到题目
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="mt-5 flex items-center justify-between">
      <span class="text-sm text-zinc-400">共 {{ total }} 题</span>
      <PaginationWhite
        v-if="totalPages > 1"
        :current-page="page"
        :total-pages="totalPages"
        @page-change="page = $event"
      />
    </div>
  </div>
</template>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.15s ease, transform 0.15s ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
</style>
