<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getContest, getProblems, createContest, updateContest, addContestProblem, type Problem } from '../../api'

const route = useRoute()
const router = useRouter()

const id = Number(route.params.id || 0)
const editing = ref(false)
const title = ref('')
const description = ref('')
const startTime = ref('')
const endTime = ref('')
const ruleType = ref('ACM')
const error = ref('')
const saving = ref(false)

const allProblems = ref<Problem[]>([])
const selectedProblems = ref<{ problem_id: number; display_id: string }[]>([])

onMounted(async () => {
  const res = await getProblems(1, 100)
  allProblems.value = res.data.problems

  if (id) {
    editing.value = true
    const contestRes = await getContest(id)
    const c = contestRes.data.contest
    if (c) {
      title.value = c.title
      description.value = c.description
      startTime.value = new Date(c.start_time).toISOString().slice(0, 16)
      endTime.value = new Date(c.end_time).toISOString().slice(0, 16)
      ruleType.value = c.rule_type
    }
  }
})

function addProblem() {
  selectedProblems.value.push({
    problem_id: 0,
    display_id: String.fromCharCode(65 + selectedProblems.value.length),
  })
}

function removeProblem(i: number) {
  selectedProblems.value.splice(i, 1)
  selectedProblems.value.forEach((p, idx) => {
    p.display_id = String.fromCharCode(65 + idx)
  })
}

async function save() {
  if (!title.value || !startTime.value || !endTime.value) {
    error.value = 'Title, start time and end time are required'
    return
  }
  saving.value = true
  error.value = ''
  try {
    const startISO = new Date(startTime.value).toISOString()
    const endISO = new Date(endTime.value).toISOString()
    const body = {
      title: title.value,
      description: description.value,
      start_time: startISO,
      end_time: endISO,
      rule_type: ruleType.value,
    }

    let contestId = id
    if (editing.value) {
      await updateContest(id, body)
    } else {
      const res = await createContest(body)
      contestId = res.data.id
    }

    for (const p of selectedProblems.value) {
      if (p.problem_id > 0) {
        await addContestProblem(contestId, p.problem_id, p.display_id)
      }
    }

    router.push('/contests')
  } catch (e: any) {
    error.value = e.response?.data?.error || 'Save failed'
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-2xl px-6 py-8">
    <div class="mb-4">
      <router-link to="/admin/dashboard" class="inline-flex items-center gap-1 text-sm text-zinc-400 hover:text-zinc-600 transition-colors dark:hover:text-zinc-300">
        <svg class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M10 19l-7-7m0 0l7-7m-7 7h18"/></svg>
        返回管理
      </router-link>
    </div>
    <h2 class="mb-6 text-xl font-bold text-zinc-900 dark:text-zinc-100">
      {{ editing ? '编辑比赛' : '新建比赛' }}
    </h2>

    <div class="space-y-5">
      <!-- Title -->
      <div class="card-premium p-5">
        <label class="mb-2 block text-sm font-semibold text-zinc-600 dark:text-zinc-400">Title</label>
        <input
          v-model="title"
          class="input-glow w-full rounded-xl border border-zinc-200 bg-zinc-50/50 px-4 py-2.5 text-sm dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200"
        />
      </div>

      <!-- Description -->
      <div class="card-premium p-5">
        <label class="mb-2 block text-sm font-semibold text-zinc-600 dark:text-zinc-400">Description</label>
        <textarea
          v-model="description"
          rows="4"
          class="input-glow w-full rounded-xl border border-zinc-200 bg-zinc-50/50 px-4 py-3 text-sm resize-none dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200"
        ></textarea>
      </div>

      <!-- Times -->
      <div class="card-premium p-5">
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="mb-2 block text-sm font-semibold text-zinc-600 dark:text-zinc-400">Start Time</label>
            <input
              v-model="startTime"
              type="datetime-local"
              class="input-glow w-full rounded-xl border border-zinc-200 bg-zinc-50/50 px-4 py-2.5 text-sm dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200"
            />
          </div>
          <div>
            <label class="mb-2 block text-sm font-semibold text-zinc-600 dark:text-zinc-400">End Time</label>
            <input
              v-model="endTime"
              type="datetime-local"
              class="input-glow w-full rounded-xl border border-zinc-200 bg-zinc-50/50 px-4 py-2.5 text-sm dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200"
            />
          </div>
        </div>
      </div>

      <!-- Rule type -->
      <div class="card-premium p-5">
        <label class="mb-2 block text-sm font-semibold text-zinc-600 dark:text-zinc-400">Rule Type</label>
        <select
          v-model="ruleType"
          class="rounded-xl border border-zinc-200 bg-zinc-50 px-4 py-2.5 text-sm font-medium text-zinc-700 shadow-sm transition-all duration-200 focus:border-brand-500 focus:outline-none focus:ring-2 focus:ring-brand-500/15 dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-300"
        >
          <option value="ACM">ACM</option>
          <option value="IOI">IOI</option>
        </select>
      </div>

      <!-- Problems -->
      <div class="card-premium p-5">
        <div class="mb-3 flex items-center justify-between">
          <label class="text-sm font-semibold text-zinc-600 dark:text-zinc-400">Contest Problems</label>
          <button
            type="button"
            class="rounded-xl border border-brand-200 bg-brand-50 px-3 py-1.5 text-xs font-semibold text-brand-600 transition-all duration-200 hover:bg-brand-100 hover:shadow-sm dark:bg-brand-900/20 dark:border-brand-800 dark:text-brand-400"
            @click="addProblem"
          >
            + Add Problem
          </button>
        </div>
        <div v-if="selectedProblems.length === 0" class="py-8 text-center text-xs text-zinc-400">
          No problems assigned yet
        </div>
        <div
          v-for="(p, i) in selectedProblems"
          :key="i"
          class="mb-2 flex items-center gap-3 rounded-xl bg-gradient-to-r from-zinc-50/80 to-zinc-50/30 p-3 dark:from-zinc-900/50 dark:to-zinc-900/20"
        >
          <span class="inline-flex h-8 w-8 items-center justify-center rounded-lg bg-brand-100 text-xs font-bold text-brand-700 dark:bg-brand-900/40 dark:text-brand-400">{{ p.display_id }}</span>
          <select
            v-model.number="p.problem_id"
            class="flex-1 rounded-xl border border-zinc-200 bg-white px-4 py-2.5 text-sm shadow-sm focus:border-brand-500 focus:outline-none focus:ring-2 focus:ring-brand-500/15 dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200"
          >
            <option :value="0" disabled>Select problem...</option>
            <option
              v-for="prob in allProblems"
              :key="prob.id"
              :value="prob.id"
            >
              #{{ prob.id }} {{ prob.title }}
            </option>
          </select>
          <button
            type="button"
            class="rounded-lg px-2 py-1 text-xs font-medium text-red-400 transition-colors hover:text-red-600"
            @click="removeProblem(i)"
          >
            Remove
          </button>
        </div>
      </div>

      <p v-if="error" class="text-sm text-red-500">{{ error }}</p>

      <div class="flex gap-3 pt-2">
        <button
          type="button"
          :disabled="saving"
          class="btn-gradient"
          @click="save"
        >
          {{ saving ? '保存中...' : 'Save Contest' }}
        </button>
        <button
          type="button"
          class="rounded-xl border border-zinc-200 bg-white px-5 py-2.5 text-sm font-medium text-zinc-600 shadow-sm transition-all duration-200 hover:bg-zinc-50 hover:shadow-md dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800"
          @click="router.back()"
        >
          Cancel
        </button>
      </div>
    </div>
  </div>
</template>
