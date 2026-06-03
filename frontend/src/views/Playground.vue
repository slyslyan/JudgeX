<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { getTemplates, runCode, type RunResult } from '../api'
import MonacoEditor from '../components/MonacoEditor.vue'

const userTemplates = ref<Record<string, string>>({})

interface PlaygroundFile {
  id: string
  name: string
  language: string
  code: string
  createdAt: number
}

const STORAGE_KEY = 'judgex-playground-files'
const ACTIVE_KEY = 'judgex-playground-active'

const LANG_CONFIG: Record<string, { label: string; color: string; ext: string; template: string }> = {
  cpp: { label: 'C++', color: 'text-blue-500', ext: 'cpp', template: '#include <iostream>\nusing namespace std;\n\nint main() {\n    // your code here\n    return 0;\n}' },
  c: { label: 'C', color: 'text-blue-400', ext: 'c', template: '#include <stdio.h>\n\nint main() {\n    // your code here\n    return 0;\n}' },
  python: { label: 'Python', color: 'text-yellow-500', ext: 'py', template: '# your code here' },
  java: { label: 'Java', color: 'text-orange-500', ext: 'java', template: 'import java.util.*;\n\npublic class Main {\n    public static void main(String[] args) {\n        // your code here\n    }\n}' },
  go: { label: 'Go', color: 'text-cyan-500', ext: 'go', template: 'package main\n\nimport "fmt"\n\nfunc main() {\n    // your code here\n}' },
  rust: { label: 'Rust', color: 'text-purple-500', ext: 'rs', template: 'fn main() {\n    // your code here\n}' },
}

const EXT_TO_LANG: Record<string, string> = {
  cpp: 'cpp', c: 'c', py: 'python', java: 'java', go: 'go', rs: 'rust',
}

const files = ref<PlaygroundFile[]>([])
const activeId = ref<string>('')
const sidebarOpen = ref(true)
const consoleVisible = ref(true)

// New file modal
const showNewModal = ref(false)
const newFileName = ref('')
const newFileLang = ref('cpp')

// Rename
const renamingId = ref<string | null>(null)
const renameText = ref('')

// Run
const debugInput = ref('')
const debugExpected = ref('')
const runningCode = ref(false)
const runResult = ref<RunResult | null>(null)
const outputMatch = ref<boolean | null>(null)

const activeFile = computed(() => files.value.find(f => f.id === activeId.value))
const langList = computed(() => Object.entries(LANG_CONFIG).map(([k, v]) => ({ value: k, ...v })))

function uid(): string {
  return Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
}

function persist() {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(files.value))
  if (activeId.value) localStorage.setItem(ACTIVE_KEY, activeId.value)
}

function openFile(id: string) {
  activeId.value = id
  persist()
}

function detectLang(name: string): string {
  const ext = name.split('.').pop()?.toLowerCase() || ''
  return EXT_TO_LANG[ext] || 'cpp'
}

function createFile() {
  const rawName = newFileName.value.trim()
  const lang = newFileLang.value
  const ext = LANG_CONFIG[lang].ext
  const name = rawName ? (rawName.includes('.') ? rawName : `${rawName}.${ext}`) : `untitled.${ext}`

  // deduplicate name
  let finalName = name
  let counter = 1
  while (files.value.some(f => f.name === finalName)) {
    const base = name.includes('.') ? name.slice(0, name.lastIndexOf('.')) : name
    finalName = `${base}_${counter}.${ext}`
    counter++
  }

  const file: PlaygroundFile = {
    id: uid(),
    name: finalName,
    language: detectLang(finalName),
    code: userTemplates.value[detectLang(finalName)] || LANG_CONFIG[detectLang(finalName)]?.template || '',
    createdAt: Date.now(),
  }
  files.value.push(file)
  activeId.value = file.id
  showNewModal.value = false
  newFileName.value = ''
  persist()
}

function removeFile(id: string) {
  const idx = files.value.findIndex(f => f.id === id)
  if (idx === -1) return
  files.value.splice(idx, 1)
  if (activeId.value === id) {
    activeId.value = files.value.length > 0
      ? files.value[Math.min(idx, files.value.length - 1)].id
      : ''
  }
  persist()
}

function startRename(id: string) {
  const f = files.value.find(x => x.id === id)
  if (!f) return
  renamingId.value = id
  renameText.value = f.name.replace(/\.[^.]+$/, '')
}

function commitRename() {
  if (!renamingId.value) return
  const f = files.value.find(x => x.id === renamingId.value)
  if (f && renameText.value.trim()) {
    const ext = f.name.includes('.') ? f.name.split('.').pop() : LANG_CONFIG[f.language].ext
    f.name = `${renameText.value.trim()}.${ext}`
    persist()
  }
  renamingId.value = null
}

function downloadFile(id: string) {
  const f = files.value.find(x => x.id === id)
  if (!f) return
  const blob = new Blob([f.code], { type: 'text/plain' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url; a.download = f.name; a.click()
  URL.revokeObjectURL(url)
}

function downloadAll() {
  for (const f of files.value) downloadFile(f.id)
}

function onCodeChange(value: string) {
  const f = activeFile.value
  if (!f) return
  f.code = value
  persist()
}

async function handleRun() {
  if (!activeFile.value) return
  runningCode.value = true
  runResult.value = null
  outputMatch.value = null
  try {
    const res = await runCode(activeFile.value.language, activeFile.value.code, debugInput.value, 5000, 256)
    runResult.value = res.data
    if (debugExpected.value.trim() && res.data.stdout.trim()) {
      const n = (s: string) => s.trim().replace(/\r\n/g, '\n').replace(/[ \t]+$/gm, '').replace(/\n+$/, '')
      outputMatch.value = n(res.data.stdout) === n(debugExpected.value)
    }
  } catch (e: any) {
    runResult.value = {
      status: 'Error',
      stdout: '',
      stderr: e.response?.data?.error || e.message || 'Run failed',
      time_used: 0, memory_used: 0,
    }
  } finally {
    runningCode.value = false
  }
}

onMounted(async () => {
  // load user templates first
  try {
    const res = await getTemplates()
    if (res.data.templates) {
      Object.assign(userTemplates.value, res.data.templates)
    }
  } catch {}

  const saved = localStorage.getItem(STORAGE_KEY)
  if (saved) {
    try {
      const parsed = JSON.parse(saved) as PlaygroundFile[]
      files.value = parsed
      const active = localStorage.getItem(ACTIVE_KEY)
      if (active && parsed.some(f => f.id === active)) activeId.value = active
      else if (parsed.length > 0) activeId.value = parsed[0].id
    } catch {}
  }
  if (files.value.length === 0) {
    const tmpl = userTemplates.value['cpp'] || LANG_CONFIG.cpp.template
    const f: PlaygroundFile = { id: uid(), name: 'main.cpp', language: 'cpp', code: tmpl, createdAt: Date.now() }
    files.value.push(f)
    activeId.value = f.id
    persist()
  }
})
</script>

<template>
  <div class="flex h-[calc(100vh-3.5rem)] flex-col bg-white dark:bg-zinc-950">
    <!-- ======== Top Bar ======== -->
    <div class="flex shrink-0 items-center gap-2 border-b border-zinc-200 px-3 py-2 dark:border-zinc-800">
      <button
        class="rounded-md p-1.5 text-zinc-500 hover:bg-zinc-100 dark:hover:bg-zinc-800"
        @click="sidebarOpen = !sidebarOpen"
        title="Toggle sidebar"
      >
        <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5"/>
        </svg>
      </button>

      <svg class="h-5 w-5 text-brand-500" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" d="M17.25 6.75L22.5 12l-5.25 5.25m-10.5 0L1.5 12l5.25-5.25m7.5-3l-4.5 16.5"/>
      </svg>
      <span class="text-sm font-semibold text-zinc-700 dark:text-zinc-300">Playground</span>

      <div class="ml-4 flex items-center gap-1">
        <button
          class="flex items-center gap-1 rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-1.5 text-xs font-medium text-zinc-600 transition hover:bg-zinc-100 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800"
          @click="showNewModal = true"
        >
          <svg class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 4.5v15m7.5-7.5h-15"/>
          </svg>
          新建文件
        </button>
      </div>

      <div v-if="files.length > 1" class="flex items-center gap-1">
        <button
          class="rounded-lg px-3 py-1.5 text-xs font-medium text-zinc-500 hover:text-zinc-700 hover:bg-zinc-100 dark:hover:bg-zinc-800 dark:hover:text-zinc-300"
          @click="downloadAll"
          title="下载全部文件"
        >
          下载全部
        </button>
      </div>

      <div class="ml-auto flex items-center gap-1">
        <button
          class="flex items-center gap-1 rounded-lg border px-3 py-1.5 text-xs font-medium transition"
          :class="consoleVisible
            ? 'border-brand-200 bg-brand-50 text-brand-600 dark:border-brand-800 dark:bg-brand-900/20 dark:text-brand-400'
            : 'border-zinc-200 bg-zinc-50 text-zinc-500 hover:bg-zinc-100 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800'"
          @click="consoleVisible = !consoleVisible"
          title="Toggle console"
        >
          <svg class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" d="M14.25 9.75L16.5 12l-2.25 2.25m-4.5 0L7.5 12l2.25-2.25M6 20.25h12A2.25 2.25 0 0020.25 18V6A2.25 2.25 0 0018 3.75H6A2.25 2.25 0 003.75 6v12A2.25 2.25 0 006 20.25z"/>
          </svg>
          {{ consoleVisible ? '关闭控制台' : '调试' }}
        </button>
      </div>
    </div>

    <!-- ======== Body ======== -->
    <div class="flex flex-1 min-h-0">
      <!-- ======== Sidebar ======== -->
      <div
        v-show="sidebarOpen"
        class="flex w-52 shrink-0 flex-col border-r border-zinc-200 bg-zinc-50/60 dark:border-zinc-800 dark:bg-zinc-900/40"
      >
        <div class="flex items-center justify-between px-3 py-2">
          <span class="text-[11px] font-bold tracking-wider text-zinc-400 uppercase">资源管理器</span>
          <span class="text-[11px] text-zinc-400">{{ files.length }}</span>
        </div>

        <div class="flex-1 overflow-y-auto px-1">
          <div
            v-for="f in files"
            :key="f.id"
            :class="[
              'group flex items-center gap-2 rounded-md px-2 py-1.5 text-xs cursor-pointer',
              f.id === activeId
                ? 'bg-brand-100 text-brand-800 dark:bg-brand-900/25 dark:text-brand-300'
                : 'text-zinc-600 hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800/60'
            ]"
            @click="openFile(f.id)"
          >
            <!-- file icon dot -->
            <span class="shrink-0 text-[10px] leading-none" :class="LANG_CONFIG[f.language]?.color || 'text-zinc-400'">●</span>

            <!-- rename or name -->
            <template v-if="renamingId === f.id">
              <input
                v-model="renameText"
                class="flex-1 min-w-0 rounded border border-brand-500 bg-white px-1 py-0.5 text-xs outline-none dark:bg-zinc-800"
                @blur="commitRename"
                @keydown.enter="commitRename"
                @keydown.escape="renamingId = null"
                autofocus
                @click.stop
              />
            </template>
            <span v-else class="flex-1 truncate">{{ f.name }}</span>

            <!-- actions on hover -->
            <div class="ml-auto flex shrink-0 gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity">
              <button
                class="rounded p-0.5 text-zinc-400 hover:text-zinc-600 hover:bg-zinc-200 dark:hover:text-zinc-300 dark:hover:bg-zinc-700"
                title="Download"
                @click.stop="downloadFile(f.id)"
              >
                <svg class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5M16.5 12L12 16.5m0 0L7.5 12m4.5 4.5V3"/>
                </svg>
              </button>
              <button
                class="rounded p-0.5 text-zinc-400 hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-900/30"
                title="Delete"
                @click.stop="removeFile(f.id)"
              >
                <svg class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12"/>
                </svg>
              </button>
            </div>
          </div>
        </div>

        <!-- quick-create buttons removed -->
      </div>

      <!-- ======== Editor area ======== -->
      <div class="flex flex-1 flex-col min-h-0">
        <!-- Tabs -->
        <div
          v-if="files.length > 0"
          class="flex shrink-0 items-center overflow-x-auto bg-zinc-50 dark:bg-zinc-900 border-b border-zinc-200 dark:border-zinc-800"
        >
          <div
            v-for="f in files"
            :key="f.id"
            :class="[
              'group flex items-center gap-1.5 px-3 py-1.5 text-xs cursor-pointer border-r border-zinc-200 dark:border-zinc-800 select-none',
              f.id === activeId
                ? 'bg-white text-zinc-800 dark:bg-zinc-950 dark:text-zinc-200 shadow-[inset_0_-2px_0_0] shadow-brand-500'
                : 'bg-zinc-50 text-zinc-500 hover:bg-zinc-100 dark:bg-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800/80'
            ]"
            @click="openFile(f.id)"
            @dblclick="startRename(f.id)"
          >
            <span class="text-[9px]" :class="LANG_CONFIG[f.language]?.color">●</span>
            <span class="whitespace-nowrap">{{ f.name }}</span>
            <button
              class="ml-1 rounded p-0.5 opacity-0 group-hover:opacity-100 hover:bg-zinc-200 hover:text-red-500 dark:hover:bg-zinc-700"
              @click.stop="removeFile(f.id)"
            >
              <svg class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12"/>
              </svg>
            </button>
          </div>

          <!-- new tab button -->
          <button
            class="shrink-0 px-2 py-1.5 text-zinc-400 hover:text-zinc-600 hover:bg-zinc-100 dark:hover:bg-zinc-800"
            title="New file"
            @click="showNewModal = true"
          >
            <svg class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 4.5v15m7.5-7.5h-15"/>
            </svg>
          </button>
        </div>

        <!-- Editor + Console -->
        <div class="flex flex-1 min-h-0">
          <!-- Editor -->
          <div v-if="activeFile" class="flex flex-1 min-h-0 flex-col">
            <MonacoEditor :model-value="activeFile.code" :language="activeFile.language" @update:model-value="onCodeChange" />
          </div>
          <div v-else class="flex flex-1 min-h-0 items-center justify-center">
            <div class="text-center">
              <p class="text-sm text-zinc-400">没有打开的文件</p>
              <button
                class="mt-3 rounded-lg border border-zinc-200 px-4 py-2 text-xs font-medium text-zinc-500 hover:bg-zinc-50 dark:border-zinc-700 dark:hover:bg-zinc-800"
                @click="showNewModal = true"
              >
                + 创建新文件
              </button>
            </div>
          </div>

          <!-- Console -->
          <div
            v-show="consoleVisible"
            class="flex w-[480px] shrink-0 min-h-0 flex-col border-l border-zinc-200 bg-zinc-50/50 dark:border-zinc-800 dark:bg-zinc-900/30"
          >
            <div class="flex shrink-0 items-center border-b border-zinc-200 px-4 py-2.5 dark:border-zinc-800">
              <span class="text-xs font-bold text-zinc-500 uppercase tracking-wider">测试控制台</span>
              <span v-if="activeFile" class="ml-2 text-[10px] text-zinc-400">— {{ activeFile.name }}</span>
            </div>

            <div class="flex flex-1 min-h-0 flex-col gap-2 p-3">
              <div class="flex flex-col flex-[3] min-h-0">
                <label class="mb-1 text-xs font-semibold text-zinc-400 uppercase tracking-wider">输入</label>
                <textarea
                  v-model="debugInput"
                  class="input-glow w-full flex-1 resize-none rounded-xl border border-zinc-200 bg-white px-3 py-2 text-sm font-mono text-zinc-700 placeholder:text-zinc-400 dark:bg-zinc-950 dark:text-zinc-300 dark:border-zinc-700"
                  placeholder="Paste your test input here..."
                ></textarea>
              </div>

              <div class="flex flex-col flex-1 min-h-0">
                <label class="mb-1 text-xs font-semibold text-zinc-400 uppercase tracking-wider">期望输出</label>
                <textarea
                  v-model="debugExpected"
                  class="input-glow w-full flex-1 resize-none rounded-xl border border-zinc-200 bg-white px-3 py-2 text-sm font-mono text-zinc-600 placeholder:text-zinc-400 dark:bg-zinc-950 dark:text-zinc-300 dark:border-zinc-700"
                  placeholder="(optional)"
                ></textarea>
              </div>

              <div class="flex flex-col flex-[3] min-h-0">
                <label class="mb-1 text-xs font-semibold text-zinc-400 uppercase tracking-wider">输出</label>
                <div
                  v-if="runningCode"
                  class="flex flex-1 items-center justify-center rounded-xl border border-zinc-200 bg-white text-sm text-zinc-400 dark:bg-zinc-950 dark:border-zinc-700"
                >
                  运行中...
                </div>
                <pre
                  v-else-if="runResult"
                  :class="[
                    'flex-1 overflow-auto rounded-xl border p-3 text-sm font-mono',
                    outputMatch === true
                      ? 'border-emerald-200 bg-emerald-50/50 text-emerald-800 dark:bg-emerald-900/20 dark:border-emerald-800 dark:text-emerald-400'
                      : outputMatch === false
                        ? 'border-red-200 bg-red-50/50 text-red-800 dark:bg-red-900/20 dark:border-red-800 dark:text-red-400'
                        : runResult.status !== 'Accepted' && runResult.status !== 'Error'
                          ? 'border-red-200 bg-red-50/50 text-red-800 dark:bg-red-900/20 dark:border-red-800 dark:text-red-400'
                          : 'border-zinc-200 bg-white text-zinc-700 dark:bg-zinc-950 dark:border-zinc-700 dark:text-zinc-300'
                  ]"
                >{{ runResult.stdout || runResult.stderr || '(no output)' }}</pre>
                <div
                  v-else
                  class="flex flex-1 items-center justify-center rounded-xl border border-zinc-200 bg-white text-sm text-zinc-400 dark:bg-zinc-950 dark:border-zinc-700"
                >
                  点击"运行"执行代码
                </div>
              </div>
            </div>

            <!-- status + run -->
            <div class="flex shrink-0 items-center gap-3 border-t border-zinc-200 px-4 py-2.5 dark:border-zinc-800">
              <button
                class="btn-gradient text-xs"
                @click="handleRun"
                :disabled="runningCode || !activeFile"
              >
                {{ runningCode ? '运行中...' : '运行' }}
              </button>

              <template v-if="runResult && outputMatch !== null">
                <span
                  :class="[
                    'inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs font-semibold shadow-sm',
                    outputMatch
                      ? 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400 dark:border-emerald-800'
                      : 'bg-red-50 text-red-700 border-red-200 dark:bg-red-900/30 dark:text-red-400 dark:border-red-800'
                  ]"
                >
                  {{ outputMatch ? '输出匹配' : '输出不匹配' }}
                </span>
              </template>
              <template v-if="runResult && outputMatch === null">
                <span
                  :class="[
                    'inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs font-semibold shadow-sm',
                    runResult.status === 'Accepted'
                      ? 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400 dark:border-emerald-800'
                      : runResult.status === 'Compile Error'
                        ? 'bg-pink-50 text-pink-700 border-pink-200 dark:bg-pink-900/30 dark:text-pink-400 dark:border-pink-800'
                        : 'bg-red-50 text-red-700 border-red-200 dark:bg-red-900/30 dark:text-red-400 dark:border-red-800'
                  ]"
                >
                  {{ runResult.status === 'Accepted' ? '完成' : runResult.status }}
                </span>
              </template>
              <span v-if="runResult" class="text-xs text-zinc-400">Time: {{ runResult.time_used }} ms</span>
              <span v-if="runResult" class="text-xs text-zinc-400">Memory: {{ runResult.memory_used }} KB</span>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- ======== 新建文件 Modal ======== -->
    <Teleport to="body">
      <div
        v-if="showNewModal"
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 backdrop-blur-sm"
        @click.self="showNewModal = false"
      >
        <div class="w-96 rounded-2xl border border-zinc-200 bg-white p-6 shadow-xl dark:border-zinc-700 dark:bg-zinc-900">
          <h3 class="text-sm font-bold text-zinc-800 dark:text-zinc-200">新建文件</h3>

          <div class="mt-4 space-y-3">
            <div>
              <label class="mb-1 block text-xs font-medium text-zinc-500">Filename</label>
              <div class="flex rounded-xl border border-zinc-200 focus-within:border-brand-500 dark:border-zinc-700">
                <input
                  v-model="newFileName"
                  class="flex-1 rounded-xl bg-transparent px-3 py-2 text-sm text-zinc-700 outline-none placeholder:text-zinc-400 dark:text-zinc-300"
                  placeholder="e.g. solution"
                  @keydown.enter="createFile"
                  autofocus
                />
                <span class="flex items-center pr-3 text-xs text-zinc-400">.{{ LANG_CONFIG[newFileLang].ext }}</span>
              </div>
            </div>

            <div>
              <label class="mb-1 block text-xs font-medium text-zinc-500">Language</label>
              <select
                v-model="newFileLang"
                class="w-full rounded-xl border border-zinc-200 bg-zinc-50 px-3 py-2 text-sm text-zinc-700 outline-none focus:border-brand-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-300"
              >
                <option v-for="l in langList" :key="l.value" :value="l.value">{{ l.label }}</option>
              </select>
            </div>
          </div>

          <div class="mt-5 flex justify-end gap-2">
            <button
              class="rounded-xl border border-zinc-200 px-4 py-2 text-xs font-medium text-zinc-600 hover:bg-zinc-50 dark:border-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800"
              @click="showNewModal = false"
            >
              取消
            </button>
            <button
              class="rounded-xl bg-brand-500 px-4 py-2 text-xs font-medium text-white hover:bg-brand-600"
              @click="createFile"
            >
              创建
            </button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- ======== Status Bar ======== -->
    <div class="flex shrink-0 items-center gap-3 border-t border-zinc-200 bg-zinc-50 px-4 py-1 text-[11px] text-zinc-400 dark:border-zinc-800 dark:bg-zinc-900">
      <span v-if="activeFile">
        <span :class="LANG_CONFIG[activeFile.language]?.color">{{ activeFile.name }}</span>
        <span class="mx-1.5 text-zinc-300 dark:text-zinc-600">·</span>
        {{ LANG_CONFIG[activeFile.language]?.label }}
      </span>
      <span v-else>未选择文件</span>
      <span class="ml-auto">{{ files.length }} 个文件</span>
    </div>
  </div>
</template>
