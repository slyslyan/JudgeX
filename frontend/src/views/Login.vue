<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { login, register } from '../api'

const router = useRouter()
const isLogin = ref(true)
const username = ref('')
const email = ref('')
const password = ref('')
const confirmPassword = ref('')
const error = ref('')

async function submit() {
  error.value = ''
  try {
    if (isLogin.value) {
      const res = await login(username.value, password.value)
      localStorage.setItem('token', res.data.token)
      localStorage.setItem('user', JSON.stringify({ id: res.data.user_id, username: res.data.username, role: res.data.role }))
    } else {
      const res = await register(username.value, email.value, password.value, confirmPassword.value)
      localStorage.setItem('token', res.data.token)
      localStorage.setItem('user', JSON.stringify({ id: res.data.user_id, username: res.data.username, role: res.data.role }))
    }
    router.push('/problems')
  } catch (e: any) {
    error.value = e.response?.data?.error || '未知错误'
  }
}
</script>

<template>
  <div class="flex min-h-[calc(100vh-3rem)] items-center justify-center px-4 bg-mesh">
    <div class="w-full max-w-sm glass-panel p-8">
      <h1 class="mb-8 text-center text-2xl font-semibold tracking-tight text-zinc-900 dark:text-white">
        {{ isLogin ? '登录' : '注册' }}
      </h1>

      <!-- Segmented control -->
      <div class="mb-6 flex rounded-full bg-zinc-100 p-0.5 dark:bg-zinc-800">
        <button
          class="flex-1 rounded-full px-3 py-1.5 text-[13px] font-medium transition-all duration-200"
          :class="isLogin ? 'bg-white text-zinc-900 shadow-sm dark:bg-zinc-600 dark:text-white' : 'text-zinc-500 dark:text-zinc-400'"
          @click="isLogin = true"
        >登录</button>
        <button
          class="flex-1 rounded-full px-3 py-1.5 text-[13px] font-medium transition-all duration-200"
          :class="!isLogin ? 'bg-white text-zinc-900 shadow-sm dark:bg-zinc-600 dark:text-white' : 'text-zinc-500 dark:text-zinc-400'"
          @click="isLogin = false"
        >注册</button>
      </div>

      <form @submit.prevent="submit" class="flex flex-col gap-3">
        <input
          v-model="username"
          placeholder="用户名"
          required
          minlength="3"
          class="apple-input"
        />
        <input
          v-if="!isLogin"
          v-model="email"
          type="email"
          placeholder="邮箱"
          required
          class="apple-input"
        />
        <input
          v-model="password"
          type="password"
          placeholder="密码"
          required
          minlength="6"
          class="apple-input"
        />
        <input
          v-if="!isLogin"
          v-model="confirmPassword"
          type="password"
          placeholder="确认密码"
          required
          class="apple-input"
        />
        <p v-if="error" class="text-[13px] text-red-500">{{ error }}</p>
        <button type="submit" class="apple-btn-primary mt-2 w-full py-2.5">
          {{ isLogin ? '登录' : '创建账号' }}
        </button>
      </form>
    </div>
  </div>
</template>
