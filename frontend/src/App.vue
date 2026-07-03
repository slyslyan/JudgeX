<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import Navbar from './components/Navbar.vue'
import Toast from './components/Toast.vue'

const route = useRoute()
const isAuthPage = computed(() => ['/login', '/register'].includes(route.path))
</script>

<template>
  <div class="min-h-screen bg-surface">
    <Navbar v-if="!isAuthPage" />
    <main>
      <router-view />
    </main>
    <footer v-if="!isAuthPage" class="text-center text-xs text-muted py-4 border-t border-border/40">
      <a href="https://beian.miit.gov.cn/" target="_blank" rel="noopener noreferrer" class="hover:underline">
        浙ICP备2026040664号-1
      </a>
    </footer>
    <Toast />
  </div>
</template>
