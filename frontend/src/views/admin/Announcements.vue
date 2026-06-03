<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { getAnnouncements, createAnnouncement, updateAnnouncement, deleteAnnouncement, type Announcement } from '../../api'

const list = ref<Announcement[]>([])
const loading = ref(true)
const showEditor = ref(false)
const editing = ref<Announcement | null>(null)
const title = ref('')
const content = ref('')

async function load() {
  loading.value = true
  try {
    const res = await getAnnouncements()
    list.value = res.data.announcements
  } catch { /* ignore */ }
  loading.value = false
}

function openCreate() {
  editing.value = null
  title.value = ''
  content.value = ''
  showEditor.value = true
}

function openEdit(a: Announcement) {
  editing.value = a
  title.value = a.title
  content.value = a.content
  showEditor.value = true
}

async function save() {
  if (!title.value.trim()) return
  try {
    if (editing.value) {
      await updateAnnouncement(editing.value.id, title.value, content.value)
    } else {
      await createAnnouncement(title.value, content.value)
    }
    showEditor.value = false
    await load()
  } catch { /* ignore */ }
}

async function remove(a: Announcement) {
  if (!confirm(`确认删除公告「${a.title}」？`)) return
  try {
    await deleteAnnouncement(a.id)
    await load()
  } catch { /* ignore */ }
}

function formatTime(iso: string) {
  const d = new Date(iso)
  return d.toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

onMounted(load)
</script>

<template>
  <div class="p-6">
    <div class="mb-6 flex items-center justify-between">
      <h1 class="text-lg font-semibold text-zinc-900 dark:text-white">公告管理</h1>
      <button class="apple-btn-primary px-4 py-2 text-[13px]" @click="openCreate">+ 新建公告</button>
    </div>

    <!-- Editor modal -->
    <div v-if="showEditor" class="fixed inset-0 z-50 flex items-center justify-center bg-black/40" @click.self="showEditor = false">
      <div class="mx-4 w-full max-w-lg rounded-2xl bg-white p-6 shadow-xl dark:bg-zinc-900">
        <h2 class="mb-4 text-[15px] font-semibold text-zinc-800 dark:text-zinc-200">{{ editing ? '编辑公告' : '新建公告' }}</h2>
        <input
          v-model="title"
          placeholder="公告标题"
          class="mb-3 w-full rounded-xl border border-zinc-200 bg-zinc-50 px-4 py-2.5 text-[14px] outline-none focus:border-brand-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-200"
        />
        <textarea
          v-model="content"
          placeholder="公告内容（支持 Markdown）"
          rows="5"
          class="mb-4 w-full resize-none rounded-xl border border-zinc-200 bg-zinc-50 px-4 py-2.5 text-[14px] outline-none focus:border-brand-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-200"
        />
        <div class="flex justify-end gap-2">
          <button class="apple-btn-secondary px-4 py-2 text-[13px]" @click="showEditor = false">取消</button>
          <button class="apple-btn-primary px-4 py-2 text-[13px]" :disabled="!title.trim()" @click="save">保存</button>
        </div>
      </div>
    </div>

    <!-- List -->
    <div v-if="loading" class="py-10 text-center text-[14px] text-zinc-400">加载中...</div>
    <div v-else-if="list.length === 0" class="py-10 text-center text-[14px] text-zinc-500 dark:text-zinc-500">暂无公告</div>
    <div v-else class="space-y-3">
      <div
        v-for="a in list"
        :key="a.id"
        class="apple-card flex items-start gap-4 p-5"
      >
        <div class="flex-1 min-w-0">
          <h3 class="text-[15px] font-semibold text-zinc-800 dark:text-zinc-200">{{ a.title }}</h3>
          <p class="mt-1 text-[13px] text-zinc-500 dark:text-zinc-400 whitespace-pre-wrap">{{ a.content }}</p>
          <p class="mt-2 text-[12px] text-zinc-400 tabular-nums">{{ formatTime(a.created_at) }}</p>
        </div>
        <div class="flex shrink-0 gap-2">
          <button class="rounded-lg px-3 py-1.5 text-[12px] font-medium text-brand-500 hover:bg-brand-50 transition-colors dark:hover:bg-brand-500/10" @click="openEdit(a)">编辑</button>
          <button class="rounded-lg px-3 py-1.5 text-[12px] font-medium text-red-500 hover:bg-red-50 transition-colors dark:hover:bg-red-500/10" @click="remove(a)">删除</button>
        </div>
      </div>
    </div>
  </div>
</template>
