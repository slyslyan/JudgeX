<script setup lang="ts">
import { computed } from 'vue'
import { marked } from 'marked'
import 'highlight.js/styles/github-dark.css'

const props = defineProps<{ content: string }>()

marked.setOptions({
  breaks: true,
  gfm: true,
})

const html = computed(() => marked(props.content || '') as string)
</script>

<template>
  <div class="markdown-body prose prose-sm max-w-none text-zinc-700 dark:text-zinc-300" v-html="html" />
</template>

<style>
/* Inline markdown styles */
.markdown-body h1 { font-size: 1.5rem; font-weight: 700; margin-top: 1.5rem; margin-bottom: 0.75rem; }
.markdown-body h2 { font-size: 1.25rem; font-weight: 600; margin-top: 1.25rem; margin-bottom: 0.5rem; }
.markdown-body h3 { font-size: 1.1rem; font-weight: 600; margin-top: 1rem; margin-bottom: 0.5rem; }
.markdown-body p { margin-bottom: 0.75rem; line-height: 1.7; }
.markdown-body ul, .markdown-body ol { padding-left: 1.5rem; margin-bottom: 0.75rem; }
.markdown-body li { margin-bottom: 0.25rem; }
.markdown-body code { font-family: var(--font-mono); font-size: 0.875em; background: #f1f5f9; padding: 0.125rem 0.375rem; border-radius: 0.25rem; }
.dark .markdown-body code { background: #27272a; }
.markdown-body pre { margin-bottom: 1rem; border-radius: 0.5rem; overflow: hidden; }
.markdown-body pre code { display: block; padding: 1rem; overflow-x: auto; font-size: 0.8125rem; line-height: 1.6; background: #1e1e2e; color: #cdd6f4; border-radius: 0.5rem; }
.markdown-body blockquote { border-left: 3px solid #3b82f6; padding-left: 1rem; margin: 0.75rem 0; color: #6b7280; }
.dark .markdown-body blockquote { color: #9ca3af; }
.markdown-body table { width: 100%; border-collapse: collapse; margin-bottom: 1rem; }
.markdown-body th, .markdown-body td { border: 1px solid #e5e7eb; padding: 0.5rem 0.75rem; text-align: left; font-size: 0.875rem; }
.dark .markdown-body th, .dark .markdown-body td { border-color: #27272a; }
.markdown-body th { background: #f9fafb; font-weight: 600; }
.dark .markdown-body th { background: #18181b; }
.markdown-body a { color: #2563eb; text-decoration: underline; }
.markdown-body strong { font-weight: 600; }
.markdown-body hr { margin: 1.5rem 0; border: 0; border-top: 1px solid #e5e7eb; }
.dark .markdown-body hr { border-color: #27272a; }
</style>
