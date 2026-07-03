<script setup lang="ts">
import { computed } from 'vue'
import { ChevronLeft, ChevronRight, MoreHorizontal } from 'lucide-vue-next'

const props = defineProps<{
  currentPage: number
  totalPages: number
}>()

const emit = defineEmits<{
  'page-change': [page: number]
}>()

const pages = computed(() => {
  const total = props.totalPages
  const current = props.currentPage
  const items: (number | 'ellipsis')[] = []

  if (total <= 7) {
    for (let i = 1; i <= total; i++) items.push(i)
    return items
  }

  items.push(1)

  if (current > 3) items.push('ellipsis')

  const start = Math.max(2, current - 1)
  const end = Math.min(total - 1, current + 1)

  for (let i = start; i <= end; i++) items.push(i)

  if (current < total - 2) items.push('ellipsis')

  items.push(total)

  return items
})

function go(n: number) {
  if (n >= 1 && n <= props.totalPages) emit('page-change', n)
}
</script>

<template>
  <nav aria-label="分页" class="flex items-center gap-1">
    <!-- Prev -->
    <button
      :disabled="currentPage <= 1"
      class="inline-flex h-8 w-8 items-center justify-center rounded-md border border-zinc-700 bg-zinc-900 text-zinc-400 transition-colors hover:bg-zinc-800 disabled:pointer-events-none disabled:opacity-40"
      @click="go(currentPage - 1)"
    >
      <ChevronLeft class="h-4 w-4" />
    </button>

    <!-- Page numbers -->
    <template v-for="item in pages" :key="item">
      <span
        v-if="item === 'ellipsis'"
        class="flex h-8 w-8 items-center justify-center"
      >
        <MoreHorizontal class="h-4 w-4 text-zinc-600" />
      </span>

      <button
        v-else
        :class="[
          'inline-flex h-8 w-8 items-center justify-center rounded-md text-sm font-medium transition-colors',
          item === currentPage
            ? 'bg-white text-black'
            : 'border border-zinc-700 bg-zinc-900 text-zinc-300 hover:bg-zinc-800',
        ]"
        @click="go(item)"
      >
        {{ item }}
      </button>
    </template>

    <!-- Next -->
    <button
      :disabled="currentPage >= totalPages"
      class="inline-flex h-8 w-8 items-center justify-center rounded-md border border-zinc-700 bg-zinc-900 text-zinc-400 transition-colors hover:bg-zinc-800 disabled:pointer-events-none disabled:opacity-40"
      @click="go(currentPage + 1)"
    >
      <ChevronRight class="h-4 w-4" />
    </button>
  </nav>
</template>
