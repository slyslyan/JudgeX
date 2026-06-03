<script setup lang="ts">
import Navbar from './components/Navbar.vue'
import Toast from './components/Toast.vue'
</script>

<template>
  <div class="min-h-screen bg-surface">
    <Navbar />
    <main>
      <router-view />
    </main>
    <Toast />
  </div>
</template>
