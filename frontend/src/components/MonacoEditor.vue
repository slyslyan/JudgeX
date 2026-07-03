<script setup lang="ts">
import { ref, onMounted, watch, onBeforeUnmount, shallowRef } from 'vue'
import * as monaco from 'monaco-editor'
import { useSyntaxChecker } from '../composables/useSyntaxChecker'

const props = defineProps<{
  modelValue: string
  language: string
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const container = ref<HTMLElement>()
const editor = shallowRef<monaco.editor.IStandaloneCodeEditor>()
const { check: checkSyntax, clear: clearSyntax } = useSyntaxChecker()

const langMap: Record<string, string> = {
  cpp: 'cpp',
  c: 'c',
  python: 'python',
  java: 'java',
  rust: 'rust',
}

let checkTimer: ReturnType<typeof setTimeout> | null = null

onMounted(() => {
  if (!container.value) return
  const isDark = document.documentElement.classList.contains('dark')
  const ed = monaco.editor.create(container.value, {
    value: props.modelValue,
    language: langMap[props.language] || 'plaintext',
    theme: isDark ? 'vs-dark' : 'vs',
    fontSize: 14,
    minimap: { enabled: false },
    automaticLayout: true,
    scrollBeyondLastLine: false,
    tabSize: 2,
  })
  ed.onDidChangeModelContent(() => {
    emit('update:modelValue', ed.getValue())
    if (checkTimer) clearTimeout(checkTimer)
    checkTimer = setTimeout(() => checkSyntax(ed, props.language), 500)
  })
  editor.value = ed
  // initial check
  checkSyntax(ed, props.language)
})

watch(() => props.language, (lang) => {
  if (editor.value) {
    const model = editor.value.getModel()
    if (model) {
      monaco.editor.setModelLanguage(model, langMap[lang] || 'plaintext')
      checkSyntax(editor.value, lang)
    }
  }
})

// When modelValue changes externally (e.g., language switch loads new draft),
// update the editor content (only if different to avoid cursor jumping)
watch(() => props.modelValue, (val) => {
  if (editor.value) {
    const current = editor.value.getValue()
    if (current !== val) {
      editor.value.setValue(val)
      checkSyntax(editor.value, props.language)
    }
  }
})

// Watch dark mode changes and update Monaco theme
let observer: MutationObserver | null = null
onMounted(() => {
  observer = new MutationObserver(() => {
    if (editor.value) {
      const isDark = document.documentElement.classList.contains('dark')
      monaco.editor.setTheme(isDark ? 'vs-dark' : 'vs')
    }
  })
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
})

onBeforeUnmount(() => {
  const model = editor.value?.getModel()
  if (model) clearSyntax(model)
  editor.value?.dispose()
  observer?.disconnect()
  if (checkTimer) clearTimeout(checkTimer)
})
</script>

<template>
  <div ref="container" class="h-full w-full"></div>
</template>
