<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import AiChat from './AiChat.vue'

const router = useRouter()
const route = useRoute()

const isDark = ref(document.documentElement.classList.contains('dark'))
const showCoach = ref(false)
const showMobileMenu = ref(false)

function toggleDark() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

function readUser() {
  const s = localStorage.getItem('user')
  return s ? JSON.parse(s) : null
}

const user = ref(readUser())
const isLoggedIn = computed(() => !!user.value)

watch(() => route.path, () => { user.value = readUser(); showMobileMenu.value = false })

function logout() {
  localStorage.removeItem('token')
  localStorage.removeItem('user')
  user.value = null
  router.push('/login')
}


const navLinks = [
  { path: '/problems', label: '题库' },
  { path: '/contests', label: '比赛' },
  { path: '/submissions', label: '提交' },
  { path: '/leaderboard', label: '排行' },
]
</script>

<template>
  <header class="sticky top-0 z-50 h-12 border-b border-black/5 bg-white/72 backdrop-blur-2xl dark:bg-[#161616]/78 dark:border-white/10">
    <div class="mx-auto flex h-full max-w-6xl items-center justify-between px-6">
      <!-- Left -->
      <div class="flex items-center gap-6">
        <span class="cursor-pointer text-base font-semibold tracking-tight text-zinc-900 dark:text-white" @click="router.push('/')">
          JudgeX
        </span>
        <nav class="hidden items-center gap-1 sm:flex">
          <router-link
            v-if="isLoggedIn"
            v-for="link in navLinks"
            :key="link.path"
            :to="link.path"
            class="rounded-full px-3.5 py-1.5 text-[13px] font-medium text-zinc-500 transition-colors hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-white"
            active-class="!text-zinc-900 dark:!text-white"
          >
            {{ link.label }}
          </router-link>
        </nav>
      </div>

      <!-- Right -->
      <div class="flex items-center gap-2">
        <router-link
          v-if="isLoggedIn"
          to="/playground"
          class="hidden items-center gap-1 rounded-full bg-zinc-100 px-3.5 py-1.5 text-[13px] font-medium text-zinc-600 transition-colors hover:bg-zinc-200 sm:flex dark:bg-zinc-800 dark:text-zinc-300 dark:hover:bg-zinc-700"
        >
          <svg class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" d="M17.25 6.75L22.5 12l-5.25 5.25m-10.5 0L1.5 12l5.25-5.25m7.5-3l-4.5 16.5"/>
          </svg>
          Playground
        </router-link>

        <button
          class="flex h-8 w-8 items-center justify-center rounded-full text-sm text-zinc-500 transition-colors hover:bg-zinc-100 sm:hidden dark:text-zinc-400 dark:hover:bg-zinc-800"
          @click="showMobileMenu = !showMobileMenu"
          aria-label="打开菜单"
        >
          <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5"/></svg>
        </button>

        <button
          class="flex h-8 w-8 items-center justify-center rounded-full text-sm text-zinc-500 transition-colors hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800"
          @click="toggleDark"
          :title="isDark ? '浅色模式' : '深色模式'"
          aria-label="切换主题"
        >
          <svg v-if="isDark" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M12 3v2.25m6.364.386l-1.591 1.591M21 12h-2.25m-.386 6.364l-1.591-1.591M12 18.75V21m-4.773-4.227l-1.591 1.591M5.25 12H3m4.227-4.773L5.636 5.636M15.75 12a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0z"/></svg>
          <svg v-else class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M21.752 15.002A9.718 9.718 0 0118 15.75c-5.385 0-9.75-4.365-9.75-9.75 0-1.33.266-2.597.748-3.752A9.753 9.753 0 003 11.25C3 16.635 7.365 21 12.75 21a9.753 9.753 0 009.002-5.998z"/></svg>
        </button>

        <template v-if="isLoggedIn">
          <button
            v-if="user?.role === 'admin' || user?.role === 'super_admin'"
            class="flex h-8 w-8 items-center justify-center rounded-full text-sm text-zinc-400 transition-colors hover:bg-zinc-100 dark:hover:bg-zinc-800"
            title="管理"
            aria-label="管理后台"
            @click="router.push('/admin/dashboard')"
          >
            <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"/>
              <path stroke-linecap="round" stroke-linejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"/>
            </svg>
          </button>

          <button
            class="flex h-8 w-8 items-center justify-center rounded-full text-sm text-zinc-400 transition-colors hover:bg-zinc-100 dark:hover:bg-zinc-800"
            title="AI 助手"
            aria-label="AI 助手"
            @click="showCoach = !showCoach"
          >
            <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 00-3.09 3.09z"/>
            </svg>
          </button>
          <AiChat
            v-if="showCoach"
            variant="dropdown"
            :options="{ agentType: 'coach' }"
            :suggestions="[
              '如何分析一道题的时间复杂度？',
              '常见的调试技巧有哪些？',
              '怎样提升算法竞赛水平？',
              '动态规划与贪心的区别是什么？'
            ]"
            @close="showCoach = false"
          />

          <button
            class="flex items-center gap-2 rounded-full py-1 pl-1.5 pr-3 text-[13px] font-medium text-zinc-600 transition-colors hover:bg-zinc-100 dark:text-zinc-300 dark:hover:bg-zinc-800"
            @click="router.push('/profile')"
          >
            <div class="flex h-6 w-6 items-center justify-center rounded-full bg-zinc-200 text-[11px] font-semibold text-zinc-600 dark:bg-zinc-700 dark:text-zinc-300">
              {{ user?.username?.charAt(0)?.toUpperCase() }}
            </div>
            <span class="hidden sm:inline">{{ user?.username }}</span>
          </button>

          <button
            class="flex h-8 w-8 items-center justify-center rounded-full text-sm text-zinc-400 transition-colors hover:bg-red-50 hover:text-red-500 dark:hover:bg-red-900/20 dark:hover:text-red-400"
            title="退出登录"
            aria-label="退出登录"
            @click="logout"
          >
            <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1"/></svg>
          </button>
        </template>

        <router-link v-else to="/login" class="apple-btn-primary text-[13px]">登录</router-link>
      </div>
    </div>
  </header>

  <!-- Mobile nav drawer -->
  <Transition name="mobile-nav">
    <div v-if="showMobileMenu && isLoggedIn" class="fixed inset-x-0 top-12 z-40 border-b border-zinc-200/60 bg-white/95 backdrop-blur-2xl shadow-lg sm:hidden dark:bg-[#161616]/95 dark:border-zinc-800">
      <nav class="flex flex-col px-4 py-3">
        <router-link
          v-for="link in navLinks"
          :key="link.path"
          :to="link.path"
          class="rounded-xl px-4 py-3 text-sm font-medium text-zinc-600 transition-colors hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800"
          active-class="!text-zinc-900 dark:!text-white !bg-zinc-100 dark:!bg-zinc-800"
          @click="showMobileMenu = false"
        >
          {{ link.label }}
        </router-link>
        <router-link
          to="/playground"
          class="rounded-xl px-4 py-3 text-sm font-medium text-zinc-600 transition-colors hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800"
          @click="showMobileMenu = false"
        >
          Playground
        </router-link>
      </nav>
    </div>
  </Transition>
</template>

<style scoped>
.mobile-nav-enter-active,
.mobile-nav-leave-active {
  transition: opacity 0.15s ease, transform 0.15s ease;
}
.mobile-nav-enter-from,
.mobile-nav-leave-to {
  opacity: 0;
  transform: translateY(-8px);
}
</style>
