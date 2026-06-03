<script setup lang="ts">
import { ref } from 'vue'

type ToastType = 'success' | 'error' | 'info'

interface ToastItem {
  id: number
  message: string
  type: ToastType
}

const toasts = ref<ToastItem[]>([])
let nextId = 0

function add(message: string, type: ToastType = 'info') {
  const id = nextId++
  toasts.value.push({ id, message, type })
  setTimeout(() => remove(id), 3500)
}

function remove(id: number) {
  toasts.value = toasts.value.filter(t => t.id !== id)
}

// Expose globally
if (typeof window !== 'undefined') {
  ;(window as any).$toast = { success: (m: string) => add(m, 'success'), error: (m: string) => add(m, 'error'), info: (m: string) => add(m, 'info') }
}

defineExpose({ success: (m: string) => add(m, 'success'), error: (m: string) => add(m, 'error'), info: (m: string) => add(m, 'info') })
</script>

<template>
  <Teleport to="body">
    <div class="pointer-events-none fixed inset-0 z-[9999] flex items-start justify-center pt-16">
      <div class="flex flex-col items-center gap-2">
        <TransitionGroup name="toast">
          <div
            v-for="t in toasts"
            :key="t.id"
            :class="{
              'bg-emerald-600 text-white': t.type === 'success',
              'bg-red-500 text-white': t.type === 'error',
              'bg-zinc-800 text-white dark:bg-zinc-200 dark:text-zinc-800': t.type === 'info',
            }"
            class="pointer-events-auto flex items-center gap-2 rounded-xl px-4 py-2.5 text-sm font-medium shadow-lg"
          >
            <span v-if="t.type === 'success'">✓</span>
            <span v-else-if="t.type === 'error'">✗</span>
            {{ t.message }}
          </div>
        </TransitionGroup>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.toast-enter-active { transition: all 0.3s ease; }
.toast-leave-active { transition: all 0.2s ease; }
.toast-enter-from { opacity: 0; transform: translateY(-12px); }
.toast-leave-to { opacity: 0; transform: translateY(-8px); }
</style>
