<template>
  <div class="flex min-h-[calc(100vh-3rem)] items-center justify-center px-4">
    <div class="text-center">
      <h1 class="text-7xl font-bold tracking-tight text-zinc-200 dark:text-zinc-700">404</h1>
      <p class="mt-4 text-lg text-zinc-500 dark:text-zinc-400">页面不存在</p>
      <router-link to="/" class="apple-btn-primary mt-8 inline-flex items-center gap-2 px-6 py-2.5 text-[15px]">
        <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M10 19l-7-7m0 0l7-7m-7 7h18"/></svg>
        返回首页
      </router-link>
    </div>
  </div>
</template>
