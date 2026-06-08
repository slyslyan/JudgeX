<script setup lang="ts">
import { ref, onMounted } from 'vue'

interface Feedback {
  id: number
  problem_id: number
  user_id: number
  submission_id: number
  feedback_type: string
  priority: string
  description: string
  evidence: string
  confidence: string
  status: string
  created_at: string
}

const feedbacks = ref<Feedback[]>([])
const loading = ref(true)
const deleting = ref<Set<number>>(new Set())

const typeLabels: Record<string, string> = {
  suspicious_testdata: '测试数据异常',
  unclear_description: '描述有歧义',
  sample_error: '样例错误',
  insufficient_coverage: '覆盖不足',
}

async function del(fb: Feedback) {
  if (!confirm(`确认删除 #${fb.problem_id} 的反馈？`)) return
  deleting.value.add(fb.id)
  try {
    const token = localStorage.getItem('token')
    await fetch(`/api/admin/problem-feedback/${fb.id}`, {
      method: 'DELETE',
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
    feedbacks.value = feedbacks.value.filter(f => f.id !== fb.id)
  } catch { /* ignore */ }
  deleting.value.delete(fb.id)
}

onMounted(async () => {
  try {
    const token = localStorage.getItem('token')
    const res = await fetch('/api/admin/problem-feedback', {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
    if (res.ok) {
      const data = await res.json()
      feedbacks.value = data.feedbacks || []
    }
  } catch { /* ignore */ }
  loading.value = false
})

</script>

<template>
  <div class="p-6">
    <div class="mb-6 flex items-center justify-between">
      <div>
        <h1 class="text-lg font-bold text-zinc-900 dark:text-zinc-100">题目质量反馈</h1>
        <p class="text-sm text-zinc-500 mt-1">AI 自动发现的题目数据或描述问题</p>
      </div>
    </div>

    <div v-if="loading" class="text-sm text-zinc-400 py-8 text-center">加载中...</div>

    <div v-else-if="feedbacks.length === 0" class="text-sm text-zinc-400 py-8 text-center">
      暂无反馈
    </div>

    <div v-else class="space-y-3">
      <div
        v-for="fb in feedbacks"
        :key="fb.id"
        class="rounded-xl border p-4"
        :class="fb.priority === 'P1'
          ? 'border-red-200 bg-red-50/30 dark:border-red-900/50 dark:bg-red-900/10'
          : 'border-amber-200 bg-amber-50/30 dark:border-amber-900/50 dark:bg-amber-900/10'"
      >
        <div class="flex items-start justify-between mb-2">
          <div class="flex items-center gap-2">
            <span
              class="rounded-md px-2 py-0.5 text-[11px] font-bold"
              :class="fb.priority === 'P1'
                ? 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
                : 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400'"
            >
              {{ fb.priority === 'P1' ? '紧急' : '一般' }}
            </span>
            <span class="rounded-md bg-zinc-100 px-2 py-0.5 text-[11px] font-medium text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400">
              #{{ fb.problem_id }}
            </span>
            <span class="text-xs font-medium text-zinc-600 dark:text-zinc-400">
              {{ typeLabels[fb.feedback_type] || fb.feedback_type }}
            </span>
          </div>
          <span class="text-[11px] text-zinc-400">{{ new Date(fb.created_at).toLocaleString() }}</span>
        </div>
        <p class="text-sm text-zinc-800 dark:text-zinc-200 mb-1.5">{{ fb.description }}</p>
        <p class="text-xs text-zinc-500 bg-white/50 dark:bg-zinc-800/50 rounded-lg p-2.5">{{ fb.evidence }}</p>
        <div class="mt-2 flex items-center gap-2">
          <span class="rounded-full px-2 py-0.5 text-[10px] font-medium"
            :class="fb.status === 'pending'
              ? 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400'
              : fb.status === 'confirmed'
              ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400'
              : 'bg-zinc-100 text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400'"
          >
            {{ fb.status === 'pending' ? '待审核' : fb.status === 'confirmed' ? '已确认' : '已忽略' }}
          </span>
          <span class="text-[11px] text-zinc-400">confidence: {{ fb.confidence }}</span>
          <button
            class="ml-auto rounded-lg px-2.5 py-1 text-[11px] font-medium text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20 transition-colors disabled:opacity-40"
            :disabled="deleting.has(fb.id)"
            @click="del(fb)"
          >删除</button>
        </div>
      </div>
    </div>
  </div>
</template>
