<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getProblem, createProblem, updateProblem } from '../../api'

const route = useRoute()
const router = useRouter()
const editing = ref(false)
const title = ref('')
const description = ref('')
const timeLimit = ref(1000)
const memoryLimit = ref(128)
const tagsInput = ref('')
const error = ref('')
const saving = ref(false)

const id = Number(route.params.id || 0)
const problemNumber = ref(0)

const examples = ref<{ input: string; output: string }[]>([
  { input: '', output: '' },
])

function addExample() {
  examples.value.push({ input: '', output: '' })
}

function removeExample(i: number) {
  examples.value.splice(i, 1)
}

onMounted(async () => {
  if (!id) return
  editing.value = true
  try {
    const res = await getProblem(id)
    title.value = res.data.title
    description.value = res.data.description
    timeLimit.value = res.data.time_limit
    memoryLimit.value = res.data.memory_limit
    problemNumber.value = res.data.number || 0
    if (res.data.sample_cases && res.data.sample_cases.length > 0) {
      examples.value = res.data.sample_cases.map((sc: any) => ({
        input: sc.input,
        output: sc.output,
      }))
    }
    if (res.data.tags && res.data.tags.length > 0) {
      tagsInput.value = res.data.tags.map((t: any) => t.name).join(', ')
    }
  } catch { /* ignore */ }
})

function buildSampleCases() {
  const cases = examples.value.filter(ex => ex.input.trim() && ex.output.trim())
  return cases.length > 0 ? cases : undefined
}

function parseTags(): string[] {
  return tagsInput.value
    .split(',')
    .map(t => t.trim())
    .filter(t => t.length > 0)
}

async function save() {
  if (!title.value.trim()) {
    error.value = 'Title is required'
    return
  }
  saving.value = true
  error.value = ''
  try {
    const sampleCases = buildSampleCases()
    const tags = parseTags()
    if (editing.value) {
      await updateProblem(id, {
        title: title.value,
        description: description.value,
        time_limit: timeLimit.value,
        memory_limit: memoryLimit.value,
        sample_cases: sampleCases,
        tags,
        number: problemNumber.value,
      } as any)
    } else {
      await createProblem(title.value, description.value, timeLimit.value, memoryLimit.value, sampleCases, tags, problemNumber.value || undefined)
    }
    router.push('/problems')
  } catch (e: any) {
    error.value = e.response?.data?.error || 'Save failed'
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-2xl px-6 py-8">
    <h2 class="mb-6 text-xl font-bold text-zinc-900 dark:text-zinc-100">
      {{ editing ? '编辑题目' : '新建题目' }}
    </h2>

    <div class="space-y-5">
      <!-- Title -->
      <div class="card-premium p-5">
        <label class="mb-2 block text-sm font-semibold text-zinc-600 dark:text-zinc-400">Title</label>
        <input
          v-model="title"
          class="input-glow w-full rounded-xl border border-zinc-200 bg-zinc-50/50 px-4 py-2.5 text-sm dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200"
          placeholder="Problem title"
        />
      </div>

      <!-- Description -->
      <div class="card-premium p-5">
        <label class="mb-2 block text-sm font-semibold text-zinc-600 dark:text-zinc-400">Description (Markdown)</label>
        <textarea
          v-model="description"
          rows="14"
          class="input-glow w-full rounded-xl border border-zinc-200 bg-zinc-50/50 px-4 py-3 text-sm font-mono resize-none dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200"
          placeholder="Problem description in Markdown..."
        ></textarea>
      </div>

      <!-- Limits -->
      <div class="card-premium p-5">
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="mb-2 block text-sm font-semibold text-zinc-600 dark:text-zinc-400">Time Limit (ms)</label>
            <input
              v-model.number="timeLimit"
              type="number"
              class="input-glow w-full rounded-xl border border-zinc-200 bg-zinc-50/50 px-4 py-2.5 text-sm dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200"
            />
          </div>
          <div>
            <label class="mb-2 block text-sm font-semibold text-zinc-600 dark:text-zinc-400">Memory Limit (MB)</label>
            <input
              v-model.number="memoryLimit"
              type="number"
              class="input-glow w-full rounded-xl border border-zinc-200 bg-zinc-50/50 px-4 py-2.5 text-sm dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200"
            />
          </div>
        </div>
      </div>

      <!-- Number -->
      <div class="card-premium p-5">
        <label class="mb-2 block text-sm font-semibold text-zinc-600 dark:text-zinc-400">编号 (Number)</label>
        <input
          v-model.number="problemNumber"
          type="number"
          min="0"
          class="input-glow w-full rounded-xl border border-zinc-200 bg-zinc-50/50 px-4 py-2.5 text-sm dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200"
          placeholder="留空自动分配"
        />
        <p class="mt-1.5 text-xs text-zinc-400">题目显示编号，留空则自动使用数据库 ID</p>
      </div>

      <!-- Tags -->
      <div class="card-premium p-5">
        <label class="mb-2 block text-sm font-semibold text-zinc-600 dark:text-zinc-400">Tags</label>
        <input
          v-model="tagsInput"
          class="input-glow w-full rounded-xl border border-zinc-200 bg-zinc-50/50 px-4 py-2.5 text-sm dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200"
          placeholder="Comma-separated: DP, Graph, Greedy, ..."
        />
        <p class="mt-1.5 text-xs text-zinc-400">Separate tags with commas, e.g. "DP, Graph, Math"</p>
      </div>

      <!-- Sample Cases -->
      <div class="card-premium p-5">
        <div class="mb-3 flex items-center justify-between">
          <label class="text-sm font-semibold text-zinc-600 dark:text-zinc-400">Sample Cases</label>
          <button
            type="button"
            class="rounded-xl border border-brand-200 bg-brand-50 px-3 py-1.5 text-xs font-semibold text-brand-600 transition-all duration-200 hover:bg-brand-100 hover:shadow-sm dark:bg-brand-900/20 dark:border-brand-800 dark:text-brand-400"
            @click="addExample"
          >
            + Add Example
          </button>
        </div>
        <div
          v-for="(ex, i) in examples"
          :key="i"
          class="mb-3 rounded-xl border border-zinc-200 bg-gradient-to-b from-zinc-50/80 to-zinc-50/30 p-4 shadow-sm dark:from-zinc-900/50 dark:to-zinc-900/20 dark:border-zinc-800"
        >
          <div class="flex items-center justify-between mb-3">
            <span class="rounded-lg bg-brand-100 px-2.5 py-1 text-xs font-bold text-brand-700 dark:bg-brand-900/40 dark:text-brand-400">Example {{ i + 1 }}</span>
            <button
              v-if="examples.length > 1"
              type="button"
              class="text-xs font-medium text-red-400 transition-colors hover:text-red-600"
              @click="removeExample(i)"
            >
              Remove
            </button>
          </div>
          <div class="grid grid-cols-2 gap-3">
            <div>
              <label class="mb-1.5 block text-xs font-semibold text-zinc-400 uppercase tracking-wider">Input</label>
              <textarea
                v-model="ex.input"
                rows="3"
                class="input-glow w-full rounded-xl border border-zinc-200 bg-white px-4 py-2.5 text-sm font-mono resize-none dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200"
              ></textarea>
            </div>
            <div>
              <label class="mb-1.5 block text-xs font-semibold text-zinc-400 uppercase tracking-wider">Expected Output</label>
              <textarea
                v-model="ex.output"
                rows="3"
                class="input-glow w-full rounded-xl border border-zinc-200 bg-white px-4 py-2.5 text-sm font-mono resize-none dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200"
              ></textarea>
            </div>
          </div>
        </div>
      </div>

      <p v-if="error" class="text-sm text-red-500">{{ error }}</p>

      <div class="flex gap-3">
        <button
          type="button"
          :disabled="saving"
          class="btn-gradient"
          @click="save"
        >
          {{ saving ? '保存中...' : 'Save' }}
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
