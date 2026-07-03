<script setup lang="ts">
defineProps<{
  to?: string
  fullWidth?: boolean
}>()
</script>

<template>
  <div
    class="relative rounded-lg"
    :class="[fullWidth ? 'flex w-full' : 'inline-flex']"
  >
    <button
      v-if="!to"
      v-bind="$attrs"
      class="glow-btn relative z-10 rounded-lg inline-flex items-center justify-center"
    >
      <slot />
    </button>
    <router-link
      v-else
      v-bind="$attrs"
      :to="to"
      class="glow-btn relative z-10 rounded-lg inline-flex items-center justify-center"
    >
      <slot />
    </router-link>
  </div>
</template>

<style scoped>
.glow-btn {
  background: #f5f5f7;
  color: #1d1d1f;
  border: 1px solid rgba(0, 0, 0, 0.1);
  transition: border-color 0.2s;
}
.glow-btn:hover:not(:disabled) {
  border-color: rgba(0, 0, 0, 0.25);
}
.glow-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

:root.dark .glow-btn {
  background: #09090b;
  color: #fafafa;
  border-color: rgba(255, 255, 255, 0.15);
}
:root.dark .glow-btn:hover:not(:disabled) {
  border-color: rgba(255, 255, 255, 0.35);
}
</style>
