<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { listUsers, updateUserRole, deleteUser } from '../../api'

interface UserInfo {
  id: number
  username: string
  email: string
  role: string
}

const users = ref<UserInfo[]>([])
const updating = ref(0)
const deleteError = ref('')

async function load() {
  const res = await listUsers()
  users.value = res.data.users
}

async function setRole(userId: number, role: string) {
  updating.value = userId
  await updateUserRole(userId, role)
  await load()
  updating.value = 0
}

async function remove(userId: number) {
  if (!confirm('确定要删除这个用户吗？此操作不可撤销。')) return
  updating.value = userId
  deleteError.value = ''
  try {
    await deleteUser(userId)
    await load()
  } catch (e: any) {
    deleteError.value = e.response?.data?.error || '删除失败'
  } finally {
    updating.value = 0
  }
}

onMounted(load)
</script>

<template>
  <div class="px-6 py-6">
    <div class="mb-4">
      <h2 class="text-lg font-semibold text-zinc-900 dark:text-white">用户管理</h2>
      <p class="mt-0.5 text-[13px] text-zinc-400">管理系统用户、分配管理员权限、删除用户</p>
    </div>

    <div v-if="deleteError" class="mb-4 rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-400">
      {{ deleteError }}
    </div>

    <div class="apple-table">
      <table class="w-full">
        <thead>
          <tr>
            <th>编号</th>
            <th>用户名</th>
            <th>邮箱</th>
            <th>角色</th>
            <th class="text-right">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="u in users" :key="u.id">
            <td class="text-sm text-zinc-400 font-mono">{{ u.id }}</td>
            <td class="text-[15px] font-semibold text-zinc-800 dark:text-zinc-200">{{ u.username }}</td>
            <td class="text-sm text-zinc-500">{{ u.email }}</td>
            <td>
              <span
                class="inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-xs font-semibold"
                :class="{
                  'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400': u.role === 'super_admin',
                  'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400': u.role === 'admin',
                  'bg-zinc-100 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400': u.role === 'user',
                }"
              >
                {{ u.role === 'super_admin' ? '超级管理员' : u.role === 'admin' ? '管理员' : '用户' }}
              </span>
            </td>
            <td class="text-right">
              <div class="flex items-center justify-end gap-1.5">
                <template v-if="u.role === 'user'">
                  <button
                    :disabled="updating === u.id"
                    class="rounded-full bg-emerald-50 px-3 py-1 text-xs font-medium text-emerald-600 transition-colors hover:bg-emerald-100 disabled:opacity-50 dark:bg-emerald-900/20 dark:text-emerald-400 dark:hover:bg-emerald-900/40"
                    @click="setRole(u.id, 'admin')"
                  >升为管理员</button>
                </template>
                <template v-else-if="u.role === 'admin'">
                  <button
                    :disabled="updating === u.id"
                    class="rounded-full bg-amber-50 px-3 py-1 text-xs font-medium text-amber-600 transition-colors hover:bg-amber-100 disabled:opacity-50 dark:bg-amber-900/20 dark:text-amber-400 dark:hover:bg-amber-900/40"
                    @click="setRole(u.id, 'user')"
                  >降为用户</button>
                </template>
                <template v-if="u.role !== 'super_admin'">
                  <button
                    :disabled="updating === u.id"
                    class="rounded-full px-3 py-1 text-xs font-medium text-red-500 transition-colors hover:bg-red-50 disabled:opacity-50 dark:hover:bg-red-900/20"
                    @click="remove(u.id)"
                  >删除</button>
                </template>
                <span v-if="u.role === 'super_admin'" class="text-xs text-zinc-400">不可操作</span>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
