<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import {
  getProblem,
  listDiskTestCases,
  deleteAllTestCases,
  addSingleTestCase,
  getSingleTestCase,
  updateSingleTestCase,
  deleteSingleTestCase,
  type DiskCaseInfo,
} from '../../api'
import axios from 'axios'

const route = useRoute()
const problemId = Number(route.params.id)
const problem = ref<any>(null)
const diskCases = ref<DiskCaseInfo[]>([])
const version = ref(0)
const loading = ref(true)
const error = ref('')

const zipFile = ref<File | null>(null)
const uploadingZip = ref(false)
const zipMessage = ref('')

const showAddForm = ref(false)
const newInput = ref('')
const newExpected = ref('')
const savingNew = ref(false)

const editingCaseId = ref<number | null>(null)
const editInput = ref('')
const editExpected = ref('')
const savingEdit = ref(false)

async function load() {
  loading.value = true
  try {
    const [p, d] = await Promise.all([
      getProblem(problemId),
      listDiskTestCases(problemId),
    ])
    problem.value = p.data
    diskCases.value = d.data.cases || []
    version.value = d.data.test_case_version
  } catch (e: any) {
    error.value = e.response?.data?.error || 'Failed to load'
  } finally {
    loading.value = false
  }
}

async function deleteAll() {
  if (!confirm('Delete ALL test cases for this problem? This cannot be undone.')) return
  try {
    await deleteAllTestCases(problemId)
    await load()
  } catch (e: any) {
    error.value = e.response?.data?.error || 'Delete failed'
  }
}

async function uploadZip() {
  if (!zipFile.value) return
  uploadingZip.value = true
  zipMessage.value = ''
  try {
    const form = new FormData()
    form.append('file', zipFile.value)
    const token = localStorage.getItem('token')
    await axios.post(`/api/admin/problems/${problemId}/testcases`, form, {
      headers: {
        'Content-Type': 'multipart/form-data',
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
    })
    zipMessage.value = 'Uploaded successfully'
    zipFile.value = null
    await load()
  } catch (e: any) {
    zipMessage.value = e.response?.data?.error || 'Upload failed'
  } finally {
    uploadingZip.value = false
  }
}

async function addNewCase() {
  if (!newInput.value.trim() || !newExpected.value.trim()) return
  savingNew.value = true
  try {
    await addSingleTestCase(problemId, newInput.value, newExpected.value)
    newInput.value = ''
    newExpected.value = ''
    showAddForm.value = false
    await load()
  } catch (e: any) {
    error.value = e.response?.data?.error || 'Add failed'
  } finally {
    savingNew.value = false
  }
}

async function startEdit(caseId: number) {
  editingCaseId.value = caseId
  try {
    const res = await getSingleTestCase(problemId, caseId)
    editInput.value = res.data.input
    editExpected.value = res.data.expected
  } catch (e: any) {
    error.value = e.response?.data?.error || 'Failed to load case'
    editingCaseId.value = null
  }
}

function cancelEdit() {
  editingCaseId.value = null
  editInput.value = ''
  editExpected.value = ''
}

async function saveEdit() {
  if (editingCaseId.value === null) return
  savingEdit.value = true
  try {
    await updateSingleTestCase(problemId, editingCaseId.value, editInput.value, editExpected.value)
    editingCaseId.value = null
    editInput.value = ''
    editExpected.value = ''
    await load()
  } catch (e: any) {
    error.value = e.response?.data?.error || 'Update failed'
  } finally {
    savingEdit.value = false
  }
}

async function deleteCase(caseId: number) {
  if (!confirm(`Delete test case #${caseId}? This cannot be undone.`)) return
  try {
    await deleteSingleTestCase(problemId, caseId)
    await load()
  } catch (e: any) {
    error.value = e.response?.data?.error || 'Delete failed'
  }
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1048576) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1048576).toFixed(1)} MB`
}

onMounted(load)
</script>

<template>
  <div class="mx-auto max-w-4xl px-6 py-8" v-icon-color>
    <div class="mb-4">
      <router-link to="/admin/dashboard" class="inline-flex items-center gap-1 text-sm text-zinc-400 hover:text-zinc-600 transition-colors dark:hover:text-zinc-300">
        <svg class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M10 19l-7-7m0 0l7-7m-7 7h18"/></svg>
        返回管理
      </router-link>
    </div>
    <div class="mb-6">
      <h2 class="text-xl font-bold text-zinc-900 dark:text-zinc-100">
        测试用例 — {{ problem?.title || '加载中...' }}
      </h2>
      <p class="mt-1 text-sm text-zinc-400">
        Problem #{{ problemId }} · {{ diskCases.length }} hidden test cases · Version {{ version }}
      </p>
    </div>

    <p v-if="error" class="mb-4 text-sm text-red-500">{{ error }}</p>

    <!-- ZIP Upload -->
    <div class="card-premium mb-5 p-5">
      <h3 class="mb-1 text-sm font-bold text-zinc-700 dark:text-zinc-300">Upload 测试用例 (ZIP)</h3>
      <p class="mb-4 text-xs text-zinc-400">
        Upload a ZIP file containing <code class="rounded-lg bg-zinc-100 px-1.5 py-0.5 text-xs font-mono dark:bg-zinc-800">1.in</code>/<code class="rounded-lg bg-zinc-100 px-1.5 py-0.5 text-xs font-mono dark:bg-zinc-800">1.out</code> pairs.
        This replaces all existing hidden test cases.
      </p>
      <div class="flex items-center gap-3">
        <input
          type="file"
          accept=".zip"
          @change="(e: any) => zipFile = e.target.files?.[0] || null"
          class="text-sm text-zinc-600 file:mr-3 file:rounded-xl file:border file:border-zinc-200 file:bg-white file:px-4 file:py-2 file:text-sm file:font-semibold file:text-zinc-600 file:shadow-sm file:transition-all hover:file:bg-zinc-50 dark:file:bg-zinc-900 dark:file:border-zinc-700 dark:file:text-zinc-400 dark:hover:file:bg-zinc-800"
        />
        <button
          :disabled="!zipFile || uploadingZip"
          class="btn-gradient"
          @click="uploadZip"
        >
          {{ uploadingZip ? 'Uploading...' : 'Upload' }}
        </button>
        <span
          v-if="zipMessage"
          :class="zipMessage.startsWith('Uploaded') ? 'text-emerald-600' : 'text-red-500'"
          class="text-xs font-medium"
        >{{ zipMessage }}</span>
      </div>
    </div>

    <!-- Add Single Test Case -->
    <div class="card-premium mb-5 p-5">
      <div class="flex items-center justify-between mb-3">
        <h3 class="text-sm font-bold text-zinc-700 dark:text-zinc-300">Add Test Case Manually</h3>
        <button
          class="rounded-xl border border-brand-200 bg-brand-50 px-3 py-1.5 text-xs font-semibold text-brand-600 transition-all duration-200 hover:bg-brand-100 dark:bg-brand-900/20 dark:border-brand-800 dark:text-brand-400"
          @click="showAddForm = !showAddForm"
        >
          {{ showAddForm ? 'Cancel' : 'Add New Case' }}
        </button>
      </div>

      <Transition name="slide-down">
        <div v-if="showAddForm" class="space-y-3">
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="mb-1.5 block text-xs font-semibold text-zinc-400 uppercase tracking-wider">Input</label>
            <textarea
              v-model="newInput"
              rows="6"
              class="input-glow w-full rounded-xl border border-zinc-200 bg-white px-4 py-3 text-sm font-mono resize-none dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200"
              placeholder="Test case input..."
            ></textarea>
          </div>
          <div>
            <label class="mb-1.5 block text-xs font-semibold text-zinc-400 uppercase tracking-wider">Expected Output</label>
            <textarea
              v-model="newExpected"
              rows="6"
              class="input-glow w-full rounded-xl border border-zinc-200 bg-white px-4 py-3 text-sm font-mono resize-none dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200"
              placeholder="Expected output..."
            ></textarea>
          </div>
        </div>
        <button
          :disabled="!newInput.trim() || !newExpected.trim() || savingNew"
          class="btn-gradient"
          @click="addNewCase"
        >
          {{ savingNew ? 'Adding...' : 'Add Test Case' }}
        </button>
      </div>
      </Transition>
    </div>

    <!-- Hidden 测试用例 on Disk -->
    <div class="table-premium dark:bg-zinc-900">
      <div class="flex items-center justify-between border-b border-zinc-100 bg-gradient-to-r from-zinc-50/80 to-zinc-50/30 px-5 py-3.5 dark:from-zinc-800/50 dark:to-zinc-800/30 dark:border-zinc-800">
        <h3 class="text-sm font-bold text-zinc-700 dark:text-zinc-300">{{ diskCases.length }} Hidden 测试用例 on Disk</h3>
        <button
          v-if="diskCases.length > 0"
          class="rounded-xl border border-red-200 bg-red-50 px-3.5 py-1.5 text-xs font-semibold text-red-500 shadow-sm transition-all duration-200 hover:bg-red-100 hover:shadow-md dark:bg-red-900/20 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-900/40"
          @click="deleteAll"
        >
          Delete All
        </button>
      </div>

      <div v-if="loading" class="py-16 text-center text-sm text-zinc-400">加载中...</div>

      <div v-else-if="diskCases.length === 0" class="py-16 text-center text-sm text-zinc-400">
        No hidden test cases yet. Upload a ZIP or add manually above.
      </div>

      <div v-else class="divide-y divide-zinc-100 dark:divide-zinc-800">
        <div
          v-for="c in diskCases"
          :key="c.case_id"
        >
          <div class="flex items-center gap-4 px-5 py-3.5">
            <span class="inline-flex h-8 w-8 items-center justify-center rounded-lg bg-brand-100 text-sm font-bold text-brand-700 dark:bg-brand-900/40 dark:text-brand-400">#{{ c.case_id }}</span>
            <span class="text-xs text-zinc-500">
              <code class="rounded-lg bg-zinc-100 px-1.5 py-0.5 font-mono dark:bg-zinc-800">{{ c.case_id }}.in</code>
              <span class="ml-1.5 text-zinc-400">{{ formatSize(c.input_size) }}</span>
            </span>
            <span class="text-xs text-zinc-500">
              <code class="rounded-lg bg-zinc-100 px-1.5 py-0.5 font-mono dark:bg-zinc-800">{{ c.case_id }}.out</code>
              <span class="ml-1.5 text-zinc-400">{{ formatSize(c.out_size) }}</span>
            </span>
            <div class="ml-auto flex items-center gap-1">
              <button
                class="rounded-xl px-3 py-1.5 text-xs font-medium text-zinc-500 transition-all duration-200 hover:bg-zinc-100 hover:text-zinc-700 dark:hover:bg-zinc-800 dark:hover:text-zinc-300"
                @click="startEdit(c.case_id)"
              >
                Edit
              </button>
              <button
                class="rounded-xl px-3 py-1.5 text-xs font-medium text-red-400 transition-all duration-200 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/30"
                @click="deleteCase(c.case_id)"
              >
                Delete
              </button>
            </div>
          </div>

          <div v-if="editingCaseId === c.case_id" class="border-t border-zinc-100 bg-gradient-to-b from-zinc-50/80 to-zinc-50/30 px-5 py-4 dark:from-zinc-900/50 dark:to-zinc-900/20 dark:border-zinc-800">
            <div class="grid grid-cols-2 gap-3 mb-3">
              <div>
                <label class="mb-1.5 block text-xs font-semibold text-zinc-400 uppercase tracking-wider">Input</label>
                <textarea
                  v-model="editInput"
                  rows="6"
                  class="input-glow w-full rounded-xl border border-zinc-200 bg-white px-4 py-3 text-sm font-mono resize-none dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200"
                ></textarea>
              </div>
              <div>
                <label class="mb-1.5 block text-xs font-semibold text-zinc-400 uppercase tracking-wider">Expected Output</label>
                <textarea
                  v-model="editExpected"
                  rows="6"
                  class="input-glow w-full rounded-xl border border-zinc-200 bg-white px-4 py-3 text-sm font-mono resize-none dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-200"
                ></textarea>
              </div>
            </div>
            <div class="flex items-center gap-2">
              <button
                :disabled="savingEdit"
                class="btn-gradient"
                @click="saveEdit"
              >
                {{ savingEdit ? '保存中...' : 'Save' }}
              </button>
              <button
                class="rounded-xl border border-zinc-200 bg-white px-4 py-2 text-sm font-medium text-zinc-600 shadow-sm transition-all duration-200 hover:bg-zinc-50 dark:bg-zinc-900 dark:border-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800"
                @click="cancelEdit"
              >
                Cancel
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
