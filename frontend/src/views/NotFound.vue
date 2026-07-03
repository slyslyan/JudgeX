<script setup lang="ts">
import Glitchy404 from '../components/Glitchy404.vue'
import GlowButton from '../components/GlowButton.vue'

const isDark = document.documentElement.classList.contains('dark')
</script>

<template>
  <div class="flex min-h-[calc(100vh-3rem)] flex-col items-center justify-center gap-6 px-4" v-icon-color>
    <Glitchy404
      width="min(60vw, 800px)"
      height="auto"
      :color="isDark ? '#ffffff' : '#000000'"
    />
    <p class="-mt-2 text-sm text-zinc-400 dark:text-zinc-500">
      页面不存在
    </p>
    <GlowButton
      to="/"
      class="gap-2 px-6 py-2.5 text-[15px] font-medium"
    >
      <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" d="M10 19l-7-7m0 0l7-7m-7 7h18" />
      </svg>
      返回首页
    </GlowButton>
  </div>
</template>
