<script setup lang="ts">
import { useRouter, useRoute } from 'vue-router'

const router = useRouter()
const route = useRoute()

const items = [
  { path: '/admin/dashboard', label: '系统监控', icon: 'M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065zM15 12a3 3 0 11-6 0 3 3 0 016 0z', role: 'admin' },
  { path: '/admin/announcements', label: '公告管理', icon: 'M11 5.882V19.24a1.76 1.76 0 01-3.417.592l-2.147-6.15M18 13a3 3 0 100-6M5.436 13.683A4.001 4.001 0 017 6h1.832c4.1 0 7.625-1.234 9.168-3v14c-1.543-1.766-5.067-3-9.168-3H7a3.988 3.988 0 01-1.564-.317z', role: 'admin' },
  { path: '/admin/users', label: '用户管理', icon: 'M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z', role: 'super_admin' },
  { path: '/admin/problem-feedback', label: '题目反馈', icon: 'M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z', role: 'admin' },
]

const user = JSON.parse(localStorage.getItem('user') || '{}')
const role = user?.role || ''

const visible = items.filter(i => i.role === 'admin' || (i.role === 'super_admin' && role === 'super_admin'))
</script>

<template>
  <div class="flex h-[calc(100vh-3rem)]">
    <!-- Sidebar -->
    <div class="w-52 shrink-0 border-r border-zinc-200/60 bg-zinc-50/50 dark:bg-zinc-900/30 dark:border-zinc-800">
      <div class="px-4 py-4">
        <h2 class="text-xs font-bold uppercase tracking-wider text-zinc-400">管理</h2>
      </div>
      <nav class="px-2">
        <button
          v-for="item in visible"
          :key="item.path"
          class="flex w-full items-center gap-2.5 rounded-xl px-3 py-2 text-sm font-medium transition-colors"
          :class="route.path.startsWith(item.path)
            ? 'bg-white text-zinc-900 shadow-sm dark:bg-zinc-800 dark:text-white'
            : 'text-zinc-500 hover:text-zinc-700 hover:bg-white/50 dark:text-zinc-400 dark:hover:text-zinc-200 dark:hover:bg-zinc-800/50'"
          @click="router.push(item.path)"
        >
          <svg class="h-4 w-4 shrink-0" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" :d="item.icon" />
          </svg>
          {{ item.label }}
        </button>
      </nav>
    </div>

    <!-- Content -->
    <div class="flex-1 overflow-y-auto">
      <router-view />
    </div>
  </div>
</template>
