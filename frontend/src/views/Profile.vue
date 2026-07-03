<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getProfile, getUserProfile, updateProfile, changePassword, getTemplates, saveTemplates, type Profile } from '../api'
import MonacoEditor from '../components/MonacoEditor.vue'

const route = useRoute()
const router = useRouter()

const profile = ref<Profile | null>(null)
const loading = ref(true)
const isOwn = computed(() => route.name === 'Profile')

const langOptions = [
  { value: 'cpp', label: 'C++' },
  { value: 'python', label: 'Python' },
  { value: 'java', label: 'Java' },
  { value: 'rust', label: 'Rust' },
]

const defaultTemplates: Record<string, string> = {
  cpp: '#include <iostream>\nusing namespace std;\n\nint main() {\n    // your code here\n    return 0;\n}',
  python: '# your code here',
  java: 'import java.util.*;\n\npublic class Main {\n    public static void main(String[] args) {\n        // your code here\n    }\n}',
  rust: 'fn main() {\n    // your code here\n}',
}

const templateLang = ref('cpp')
const templateCode = ref(defaultTemplates.cpp)
const userTemplates = ref<Record<string, string>>({})
const savingTemplate = ref(false)
const templateMsg = ref('')

watch(templateLang, (newLang, oldLang) => {
  if (oldLang) {
    userTemplates.value[oldLang] = templateCode.value
  }
  templateCode.value = userTemplates.value[newLang] || defaultTemplates[newLang]
})

async function saveTemplate() {
  savingTemplate.value = true
  templateMsg.value = ''
  try {
    userTemplates.value[templateLang.value] = templateCode.value
    await saveTemplates(userTemplates.value)
    templateMsg.value = '已保存'
    setTimeout(() => templateMsg.value = '', 2000)
  } catch (e: any) {
    templateMsg.value = e.response?.data?.error || '保存失败'
  } finally {
    savingTemplate.value = false
  }
}

onMounted(async () => {
  try {
    const userId = route.params.id
    profile.value = userId
      ? (await getUserProfile(Number(userId))).data
      : (await getProfile()).data
  } catch {
    router.push('/')
  } finally {
    loading.value = false
  }

  if (isOwn.value) {
    try {
      const res = await getTemplates()
      if (res.data.templates) {
        userTemplates.value = res.data.templates
        templateCode.value = res.data.templates[templateLang.value] || defaultTemplates[templateLang.value]
      }
    } catch (e) { console.error('Failed to load templates:', e) }
  }
})

watch(profile, (p) => {
  if (p) {
    email.value = p.email || ''
    bio.value = p.bio || ''
  }
}, { immediate: true })

function acceptanceRate(): string {
  if (!profile.value || profile.value.total_submissions === 0) return '--'
  return ((profile.value.accepted_submissions / profile.value.total_submissions) * 100).toFixed(1) + '%'
}

function formatDate(t: string): string {
  return new Date(t).toLocaleDateString('en-US', { year: 'numeric', month: 'short', day: 'numeric' })
}

const statusStyle = (s: string) => {
  if (s === 'Accepted') return 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400 dark:border-emerald-800'
  if (s === 'Wrong Answer') return 'bg-red-50 text-red-700 border-red-200 dark:bg-red-900/30 dark:text-red-400 dark:border-red-800'
  if (s === 'Time Limit Exceeded') return 'bg-amber-50 text-amber-700 border-amber-200 dark:bg-amber-900/30 dark:text-amber-400 dark:border-amber-800'
  if (s === 'Runtime Error') return 'bg-pink-50 text-pink-700 border-pink-200 dark:bg-pink-900/30 dark:text-pink-400 dark:border-pink-800'
  if (s === 'Compile Error') return 'bg-pink-50 text-pink-700 border-pink-200 dark:bg-pink-900/30 dark:text-pink-400 dark:border-pink-800'
  if (s === 'pending' || s === 'judging') return 'bg-blue-50 text-blue-700 border-blue-200 dark:bg-blue-900/30 dark:text-blue-400 dark:border-blue-800'
  return 'bg-zinc-50 text-zinc-600 border-zinc-200 dark:bg-zinc-800 dark:text-zinc-400 dark:border-zinc-700'
}

function roleBadge(role: string) {
  if (role === 'super_admin') return 'bg-gradient-to-br from-amber-50 to-amber-100 text-amber-700 border-amber-200 shadow-sm shadow-amber-200/50 dark:from-amber-900/20 dark:text-amber-400 dark:border-amber-800'
  if (role === 'admin') return 'bg-gradient-to-br from-blue-50 to-blue-100 text-blue-700 border-blue-200 shadow-sm shadow-blue-200/50 dark:from-blue-900/20 dark:text-blue-400 dark:border-blue-800'
  return 'bg-zinc-50 text-zinc-600 border-zinc-200 dark:bg-zinc-800 dark:text-zinc-400 dark:border-zinc-700'
}

const email = ref('')
const bio = ref('')
const saving = ref(false)
const saveMsg = ref('')

const oldPw = ref('')
const newPw = ref('')
const confirmPw = ref('')
const changingPw = ref(false)
const pwMsg = ref('')

async function saveAll() {
  if (!profile.value) return
  saving.value = true
  saveMsg.value = ''
  try {
    await updateProfile({ email: email.value, bio: bio.value })
    profile.value.email = email.value
    profile.value.bio = bio.value
    saveMsg.value = '资料已保存'
    setTimeout(() => saveMsg.value = '', 2000)
  } catch (e: any) {
    saveMsg.value = e.response?.data?.error || '保存失败'
  } finally {
    saving.value = false
  }
}

async function changePw() {
  pwMsg.value = ''
  if (newPw.value !== confirmPw.value) { pwMsg.value = '两次密码不一致'; return }
  if (newPw.value.length < 6) { pwMsg.value = '至少6个字符'; return }
  changingPw.value = true
  try {
    const res = await changePassword(oldPw.value, newPw.value)
    localStorage.setItem('token', res.data.token)
    pwMsg.value = '密码已修改'
    oldPw.value = ''; newPw.value = ''; confirmPw.value = ''
    setTimeout(() => pwMsg.value = '', 2000)
  } catch (e: any) {
    pwMsg.value = e.response?.data?.error || 'Failed'
  } finally {
    changingPw.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-4xl px-6 py-8" v-icon-color>
    <div v-if="loading" class="py-16 text-center text-sm text-zinc-400">加载中...</div>

    <template v-else-if="profile">
      <!-- Header -->
      <div class="card-premium mb-8 p-6">
        <div class="flex items-center gap-5">
          <div class="flex h-16 w-16 items-center justify-center rounded-full bg-gradient-to-br from-brand-400 to-brand-600 text-2xl font-bold text-white shadow-lg shadow-brand-500/25 shrink-0">
            {{ profile.username.charAt(0).toUpperCase() }}
          </div>
          <div class="flex-1">
            <div class="flex items-center gap-3 flex-wrap">
              <h1 class="text-2xl font-bold text-zinc-900 dark:text-zinc-100">{{ profile.username }}</h1>
              <span :class="roleBadge(profile.role)" class="inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs font-semibold">
                <svg v-if="profile.role === 'super_admin'" class="h-3 w-3" fill="currentColor" viewBox="0 0 20 20"><path fill-rule="evenodd" d="M9.049 2.927c.3-.921 1.603-.921 1.902 0l1.07 3.292a1 1 0 00.95.69h3.462c.969 0 1.371 1.24.588 1.81l-2.8 2.034a1 1 0 00-.364 1.118l1.07 3.292c.3.921-.755 1.688-1.54 1.118l-2.8-2.034a1 1 0 00-1.175 0l-2.8 2.034c-.784.57-1.838-.197-1.539-1.118l1.07-3.292a1 1 0 00-.364-1.118L2.98 8.72c-.783-.57-.38-1.81.588-1.81h3.461a1 1 0 00.951-.69l1.07-3.292z" clip-rule="evenodd"/></svg>
                {{ profile.role === 'super_admin' ? '超级管理员' : profile.role === 'admin' ? 'Admin' : 'User' }}
              </span>
            </div>
            <p v-if="profile.bio" class="mt-2 text-sm text-zinc-500 dark:text-zinc-400 italic">"{{ profile.bio }}"</p>
            <div class="mt-2 flex gap-4 text-sm text-zinc-400">
              <span v-if="profile.email" class="inline-flex items-center gap-1.5">
                <svg class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"/><polyline points="22,6 12,13 2,6"/></svg>
                {{ profile.email }}
              </span>
              <span class="inline-flex items-center gap-1.5">
                <svg class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><path stroke-linecap="round" d="M12 6v6l4 2"/></svg>
                Joined {{ formatDate(profile.created_at) }}
              </span>
            </div>
          </div>
        </div>
      </div>

      <!-- Edit Profile (own profile only) -->
      <div v-if="isOwn" class="card-premium mb-8 overflow-hidden">
        <div class="border-b border-zinc-100 bg-gradient-to-r from-zinc-50/80 to-zinc-50/30 px-5 py-3.5 dark:from-zinc-800/50 dark:to-zinc-800/30 dark:border-zinc-800">
          <h2 class="text-sm font-bold text-zinc-700 dark:text-zinc-300">Edit Profile</h2>
        </div>
        <div class="p-5 space-y-5">
          <div class="grid gap-4 max-w-lg">
            <div>
              <label class="mb-1.5 block text-xs font-semibold text-zinc-400 uppercase tracking-wider">邮箱</label>
              <input v-model="email" type="email" placeholder="你的邮箱"
                class="input-glow w-full rounded-xl border border-zinc-200 bg-zinc-50/50 px-4 py-2.5 text-sm dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200" />
            </div>
            <div>
              <label class="mb-1.5 block text-xs font-semibold text-zinc-400 uppercase tracking-wider">简介</label>
              <textarea v-model="bio" placeholder="个人简介，展示在你的资料页上..." rows="2" maxlength="512"
                class="input-glow w-full rounded-xl border border-zinc-200 bg-zinc-50/50 px-4 py-2.5 text-sm resize-none dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200" />
            </div>
            <div class="flex items-center gap-3">
              <button :disabled="saving" @click="saveAll"
                class="btn-gradient">
                {{ saving ? 'Saving...' : 'Save' }}
              </button>
              <span v-if="saveMsg" class="text-xs font-medium text-emerald-600">{{ saveMsg }}</span>
            </div>
          </div>

          <div class="border-t border-zinc-100 pt-5 dark:border-zinc-800">
            <h3 class="mb-3 text-xs font-bold uppercase tracking-wider text-zinc-400">Change Password</h3>
            <div class="grid gap-3 max-w-sm">
              <input v-model="oldPw" type="password" placeholder="当前密码"
                class="input-glow rounded-xl border border-zinc-200 bg-zinc-50/50 px-4 py-2.5 text-sm dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200" />
              <input v-model="newPw" type="password" placeholder="新密码（至少6位）"
                class="input-glow rounded-xl border border-zinc-200 bg-zinc-50/50 px-4 py-2.5 text-sm dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200" />
              <input v-model="confirmPw" type="password" placeholder="确认新密码" @keyup.enter="changePw"
                class="input-glow rounded-xl border border-zinc-200 bg-zinc-50/50 px-4 py-2.5 text-sm dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200" />
              <div class="flex items-center gap-3">
                <button :disabled="changingPw || !oldPw || !newPw || !confirmPw" @click="changePw"
                  class="btn-gradient">
                  {{ changingPw ? '修改中...' : 'Change Password' }}
                </button>
                <span v-if="pwMsg" :class="pwMsg.includes('changed') ? 'text-emerald-600' : 'text-red-500'" class="text-xs font-medium">{{ pwMsg }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Code Templates (own profile only) -->
      <div v-if="isOwn" class="card-premium mb-8 overflow-hidden">
        <div class="border-b border-zinc-100 bg-gradient-to-r from-zinc-50/80 to-zinc-50/30 px-5 py-3.5 dark:from-zinc-800/50 dark:to-zinc-800/30 dark:border-zinc-800">
          <h2 class="text-sm font-bold text-zinc-700 dark:text-zinc-300">Code Templates</h2>
        </div>
        <div class="p-5 space-y-4">
          <div class="flex items-center gap-3">
            <label class="text-xs font-semibold text-zinc-400 uppercase tracking-wider">语言</label>
            <select
              v-model="templateLang"
              class="rounded-xl border border-zinc-200 bg-zinc-50 px-4 py-2 text-sm font-medium text-zinc-700 shadow-sm transition-all duration-200 focus:border-brand-500 focus:outline-none focus:ring-2 focus:ring-brand-500/15"
            >
              <option v-for="opt in langOptions" :key="opt.value" :value="opt.value">
                {{ opt.label }}
              </option>
            </select>
          </div>
          <div class="h-64 rounded-xl border border-zinc-200 overflow-hidden shadow-sm dark:border-zinc-700">
            <MonacoEditor v-if="templateCode" v-model="templateCode" :language="templateLang" />
          </div>
          <div class="flex items-center gap-3">
            <button :disabled="savingTemplate" @click="saveTemplate"
              class="btn-gradient">
              {{ savingTemplate ? 'Saving...' : 'Save Template' }}
            </button>
            <span v-if="templateMsg" :class="templateMsg === '已保存' ? 'text-emerald-600' : 'text-red-500'" class="text-xs font-medium">{{ templateMsg }}</span>
          </div>
        </div>
      </div>

      <!-- Stats -->
      <div class="mb-8 grid grid-cols-3 gap-4">
        <div class="stat-card text-center">
          <div class="text-3xl font-bold text-gradient-brand">{{ profile.solved_problems }}</div>
          <div class="mt-1 text-xs font-semibold text-zinc-400 uppercase tracking-wider">通过数</div>
        </div>
        <div class="stat-card text-center" style="border-left-color: #10b981;">
          <div class="text-3xl font-bold text-emerald-600">{{ profile.accepted_submissions }}</div>
          <div class="mt-1 text-xs font-semibold text-zinc-400 uppercase tracking-wider">Accepted</div>
        </div>
        <div class="stat-card text-center" style="border-left-color: #6366f1;">
          <div class="text-3xl font-bold text-zinc-700 dark:text-zinc-300">{{ acceptanceRate() }}</div>
          <div class="mt-1 text-xs font-semibold text-zinc-400 uppercase tracking-wider">Rate</div>
        </div>
      </div>

      <!-- Submissions -->
      <div>
        <h2 class="mb-4 text-lg font-bold text-zinc-800 dark:text-zinc-200">Recent 提交记录</h2>
        <div class="table-premium dark:bg-zinc-900">
          <table class="w-full">
            <thead>
              <tr class="border-b border-zinc-100 bg-gradient-to-r from-zinc-50/80 to-zinc-50/30 dark:from-zinc-800/50 dark:to-zinc-800/30 dark:border-zinc-800">
                <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400">编号</th>
                <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400">题目</th>
                <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400">语言</th>
                <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400">状态</th>
                <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400">时间</th>
                <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400">内存</th>
                <th class="px-4 py-3.5 text-left text-xs font-semibold uppercase tracking-wider text-zinc-400">日期</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-zinc-100 dark:divide-zinc-800">
              <tr v-for="s in profile.recent_submissions" :key="s.id"
                class="cursor-pointer transition-all duration-200 hover:bg-brand-50/30 dark:hover:bg-zinc-800/50"
                @click="router.push(`/submissions/${s.id}`)">
                <td class="px-4 py-3.5 text-sm font-mono text-zinc-400">#{{ s.id }}</td>
                <td class="px-4 py-3.5 text-sm font-medium text-zinc-700 transition-colors hover:text-brand-600 dark:text-zinc-300">{{ s.problem_title || `#${s.problem_id}` }}</td>
                <td class="px-4 py-3.5">
                  <span class="inline-flex rounded-lg bg-zinc-100 px-2 py-0.5 text-xs font-medium text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">{{ s.language }}</span>
                </td>
                <td class="px-4 py-3.5">
                  <span :class="statusStyle(s.status)" class="inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs font-semibold">
                    {{ s.status }}
                  </span>
                </td>
                <td class="px-4 py-3.5 text-sm text-zinc-500">{{ s.time_used }} ms</td>
                <td class="px-4 py-3.5 text-sm text-zinc-500">{{ s.memory_used }} KB</td>
                <td class="px-4 py-3.5 text-sm text-zinc-400">{{ formatDate(s.created_at) }}</td>
              </tr>
            </tbody>
          </table>
          <p v-if="profile.recent_submissions.length === 0" class="py-16 text-center text-sm text-zinc-400">暂无提交</p>
        </div>
      </div>
    </template>
  </div>
</template>
