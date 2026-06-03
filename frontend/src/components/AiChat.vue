<script setup lang="ts">
import { ref, nextTick, watch, computed } from 'vue'
import { useAiChat, type AiChatOptions } from '../composables/useAiChat'
import MarkdownRenderer from './MarkdownRenderer.vue'

const props = defineProps<{
  options: AiChatOptions
  reveal?: { agentType?: string; message?: string } | null
  suggestions?: string[]
  variant?: 'floating' | 'dropdown' | 'inline'
}>()

const { messages, streaming, error, send, abort, clear, updateOptions } = useAiChat(props.options)

const input = ref('')
const chatContainer = ref<HTMLElement | null>(null)

const agentTypes = [
  { value: 'coach', label: 'Virtual Coach' },
  { value: 'diagnose', label: 'Error Diagnose' },
  { value: 'socratic', label: 'Socratic Guide' },
]

const agentIcons: Record<string, string> = {
  coach: '🧑‍🏫',
  diagnose: '🔍',
  socratic: '💡',
}

const agentLabels: Record<string, string> = {
  coach: 'Virtual Coach',
  diagnose: 'Error Diagnose',
  socratic: 'Socratic Guide',
}

const selectedAgent = ref(props.options.agentType || 'coach')

watch(selectedAgent, (val) => {
  updateOptions({ agentType: val })
})

watch(() => props.options, (opts) => {
  updateOptions(opts)
  if (opts.agentType) selectedAgent.value = opts.agentType
}, { deep: true })

async function handleSend() {
  const text = input.value.trim()
  if (!text) return
  input.value = ''
  await send(text)
  await nextTick()
  scrollToBottom()
}

function handleKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    handleSend()
  }
}

function scrollToBottom() {
  if (chatContainer.value) {
    chatContainer.value.scrollTop = chatContainer.value.scrollHeight
  }
}

watch(
  () => messages.value.length,
  () => nextTick().then(scrollToBottom)
)

watch(
  () => streaming.value,
  () => nextTick().then(scrollToBottom)
)

const variant = computed(() => props.variant || 'floating')
const isOpen = ref(variant.value === 'dropdown' || variant.value === 'inline')

function toggle() {
  isOpen.value = !isOpen.value
  if (isOpen.value) {
    nextTick().then(scrollToBottom)
  }
}

function close() {
  isOpen.value = false
}

const emit = defineEmits<{ close: [] }>()

watch(
  () => props.reveal,
  (r) => {
    if (!r) return
    isOpen.value = true
    if (r.agentType) {
      selectedAgent.value = r.agentType
      updateOptions({ agentType: r.agentType })
    }
    if (r.message) {
      nextTick().then(() => {
        input.value = r.message!
        handleSend()
      })
    }
  },
  { deep: true }
)
</script>

<template>
  <!-- Floating toggle button -->
  <button
    v-if="!isOpen && variant === 'floating'"
    class="fixed bottom-6 right-6 z-50 flex h-14 w-14 items-center justify-center rounded-2xl bg-gradient-to-br from-brand-500 to-brand-700 text-white shadow-lg shadow-brand-500/25 transition-all hover:scale-105 hover:shadow-xl hover:shadow-brand-500/30"
    title="AI Assistant"
    @click="toggle"
  >
    <svg class="h-6 w-6" fill="none" stroke="currentColor" stroke-width="1.8" viewBox="0 0 24 24">
      <path stroke-linecap="round" stroke-linejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 00-3.09 3.09zM18.259 8.715L18 9.75l-.259-1.035a3.375 3.375 0 00-2.455-2.456L14.25 6l1.036-.259a3.375 3.375 0 002.455-2.456L18 2.25l.259 1.035a3.375 3.375 0 002.455 2.456L21.75 6l-1.036.259a3.375 3.375 0 00-2.455 2.456z" />
    </svg>
  </button>

  <!-- Chat panel -->
  <div
    v-if="isOpen"
    :class="[
      variant === 'floating'
        ? 'fixed bottom-6 right-6 z-50'
        : variant === 'dropdown'
          ? 'absolute right-0 top-full z-50 mt-2'
          : '',
      variant === 'inline'
        ? 'flex flex-col h-full rounded-2xl border border-zinc-200 bg-white overflow-hidden'
        : 'flex w-[420px] flex-col rounded-2xl border border-zinc-200 bg-white shadow-2xl shadow-zinc-900/10 overflow-hidden'
    ]"
    :style="variant === 'floating' ? 'height: 600px; max-height: calc(100vh - 100px)' : variant === 'dropdown' ? 'height: 520px; max-height: 80vh' : ''"
  >
    <!-- Header -->
    <div class="shrink-0 border-b border-zinc-100 bg-white px-5 py-3.5">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-3">
          <div class="flex h-9 w-9 items-center justify-center rounded-xl bg-brand-50 text-lg">
            {{ agentIcons[selectedAgent] }}
          </div>
          <div>
            <div class="text-sm font-semibold text-zinc-800 leading-tight">AI Assistant</div>
            <select
              v-model="selectedAgent"
              class="mt-0.5 rounded-md border border-zinc-200 bg-zinc-50 px-1.5 py-0 text-[11px] font-medium text-zinc-500 focus:outline-none focus:ring-1 focus:ring-brand-500/30"
            >
              <option v-for="a in agentTypes" :key="a.value" :value="a.value" class="text-zinc-800">
                {{ a.label }}
              </option>
            </select>
          </div>
        </div>
        <div class="flex items-center gap-0.5">
          <button
            :disabled="!messages.length"
            class="rounded-lg px-2 py-1.5 text-xs font-medium text-zinc-400 transition-colors hover:bg-zinc-100 hover:text-zinc-600 disabled:opacity-30"
            title="Clear chat"
            @click="clear"
          >
            Clear
          </button>
          <button
            class="rounded-lg px-2 py-1.5 text-zinc-400 transition-colors hover:bg-zinc-100 hover:text-zinc-600"
            title="Close"
            @click="close(); emit('close')"
          >
            <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>
      </div>
    </div>

    <!-- Messages -->
    <div ref="chatContainer" class="flex-1 overflow-y-auto bg-zinc-50/50">
      <!-- Empty state -->
      <div v-if="messages.length === 0" class="flex flex-col items-center py-12 px-6">
        <div class="flex h-16 w-16 items-center justify-center rounded-2xl bg-gradient-to-br from-brand-50 to-brand-100 text-3xl shadow-sm">
          {{ agentIcons[selectedAgent] }}
        </div>
        <p class="mt-4 text-sm font-semibold text-zinc-700">{{ agentLabels[selectedAgent] }}</p>
        <p class="mt-1 text-xs text-zinc-400 text-center leading-relaxed">Ask me anything about this problem. I can help diagnose errors, guide your thinking, or answer questions.</p>
        <div v-if="props.suggestions && props.suggestions.length" class="mt-5 flex flex-wrap justify-center gap-2 w-full">
          <button
            v-for="s in props.suggestions"
            :key="s"
            :disabled="streaming"
            class="rounded-full border border-zinc-200 bg-white px-3.5 py-1.5 text-xs font-medium text-zinc-500 transition-all hover:border-brand-300 hover:bg-brand-50 hover:text-brand-600 hover:shadow-sm disabled:opacity-50"
            @click="input = s; handleSend()"
          >
            {{ s }}
          </button>
        </div>
      </div>

      <!-- Message bubbles -->
      <div class="px-4 py-4 space-y-4">
        <div
          v-for="(msg, i) in messages"
          :key="i"
          class="flex gap-3"
          :class="msg.role === 'user' ? 'flex-row-reverse' : ''"
        >
          <!-- Avatar -->
          <div
            class="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-xs"
            :class="msg.role === 'user'
              ? 'bg-brand-100 text-brand-600'
              : 'bg-gradient-to-br from-brand-500 to-brand-600 text-white'"
          >
            {{ msg.role === 'user' ? 'U' : agentIcons[selectedAgent] }}
          </div>

          <!-- Bubble -->
          <div
            :class="[
              'max-w-[80%] rounded-2xl px-4 py-2.5 text-sm leading-relaxed',
              msg.role === 'user'
                ? 'bg-brand-600 text-white rounded-tr-md'
                : 'bg-white border border-zinc-100 text-zinc-700 rounded-tl-md shadow-sm'
            ]"
          >
            <MarkdownRenderer v-if="msg.content" :content="msg.content" />
            <span
              v-if="i === messages.length - 1 && streaming && msg.role === 'assistant'"
              class="ml-0.5 inline-block h-4 w-0.5 animate-pulse bg-brand-500 align-text-bottom rounded-full"
            >&nbsp;</span>
          </div>
        </div>
      </div>

      <!-- Error -->
      <div v-if="error" class="mx-4 mb-4 rounded-xl bg-red-50 border border-red-100 px-4 py-3 text-xs text-red-600 font-medium">
        {{ error }}
      </div>
    </div>

    <!-- Input -->
    <div class="shrink-0 border-t border-zinc-100 bg-white px-4 py-3">
      <div class="flex items-end gap-2">
        <textarea
          v-model="input"
          :disabled="streaming"
          rows="1"
          class="max-h-32 min-h-[38px] flex-1 resize-none rounded-xl border border-zinc-200 bg-zinc-50 px-4 py-2 text-sm text-zinc-700 placeholder:text-zinc-300 focus:border-brand-400 focus:bg-white focus:outline-none focus:ring-2 focus:ring-brand-500/10 transition-colors disabled:bg-zinc-100"
          placeholder="Type a message..."
          @keydown="handleKeydown"
          @input="(e: any) => { e.target.style.height = 'auto'; e.target.style.height = Math.min(e.target.scrollHeight, 128) + 'px' }"
        ></textarea>
        <button
          v-if="streaming"
          class="flex-shrink-0 rounded-xl bg-red-500 px-4 py-2 text-sm font-semibold text-white transition-all hover:bg-red-600 active:scale-95"
          @click="abort"
        >
          Stop
        </button>
        <button
          v-else
          :disabled="!input.trim()"
          class="flex-shrink-0 rounded-xl bg-brand-600 px-4 py-2 text-sm font-semibold text-white transition-all hover:bg-brand-700 active:scale-95 disabled:bg-zinc-200 disabled:text-zinc-400"
          @click="handleSend"
        >
          <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2.5" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" d="M6 12h12m0 0l-5-5m5 5l-5 5" />
          </svg>
        </button>
      </div>
      <p class="mt-1.5 text-[10px] text-zinc-300 text-center">
        {{ streaming ? 'AI is generating...' : 'Press Enter to send' }}
      </p>
    </div>
  </div>
</template>
