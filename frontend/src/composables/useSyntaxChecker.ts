import * as monaco from 'monaco-editor'

interface SyntaxError {
  message: string
  line: number
  column: number
  endLine: number
  endColumn: number
}

const bracketNames: Record<string, string> = {
  '{': "brace '{'",
  '}': "brace '}'",
  '[': "bracket '['",
  ']': "bracket ']'",
  '(': "parenthesis '('",
  ')': "parenthesis ')'",
}

function checkCommon(code: string): SyntaxError[] {
  const errors: SyntaxError[] = []
  const lines = code.split('\n')
  const stack: { char: string; line: number; col: number }[] = []
  const opener: Record<string, string> = { '{': '}', '[': ']', '(': ')' }
  const closer: Record<string, string> = { '}': '{', ']': '[', ')': '(' }

  let inBlockComment = false
  let stringDelim: string | null = null
  let inLineComment = false

  for (let lineIdx = 0; lineIdx < lines.length; lineIdx++) {
    const line = lines[lineIdx]
    inLineComment = false

    for (let colIdx = 0; colIdx < line.length; colIdx++) {
      const ch = line[colIdx]
      const next = colIdx + 1 < line.length ? line[colIdx + 1] : null
      const prev = colIdx > 0 ? line[colIdx - 1] : null

      // Escape sequence inside string — skip next char
      if (stringDelim !== null && prev === '\\') continue

      // Inside a string literal
      if (stringDelim !== null) {
        if (ch === stringDelim) stringDelim = null
        continue
      }

      // Inside block comment
      if (inBlockComment) {
        if (ch === '*' && next === '/') { inBlockComment = false; colIdx++ }
        continue
      }

      // Inside line comment
      if (inLineComment) continue

      // Start of comment
      if (ch === '/' && next === '/') { inLineComment = true; continue }
      if (ch === '/' && next === '*') { inBlockComment = true; colIdx++; continue }

      // Start of string literal
      if (ch === '"' || ch === "'") { stringDelim = ch; continue }

      // Bracket matching
      if (ch in opener) {
        stack.push({ char: ch, line: lineIdx + 1, col: colIdx + 1 })
      } else if (ch in closer) {
        const expected = closer[ch]
        if (stack.length === 0 || stack[stack.length - 1].char !== expected) {
          errors.push({
            message: `Unmatched closing ${bracketNames[ch] || ch}`,
            line: lineIdx + 1,
            column: colIdx + 1,
            endLine: lineIdx + 1,
            endColumn: colIdx + 2,
          })
        } else {
          stack.pop()
        }
      }
    }
  }

  // Unclosed brackets
  for (const s of stack) {
    errors.push({
      message: `Unclosed ${bracketNames[s.char] || s.char}`,
      line: s.line,
      column: s.col,
      endLine: s.line,
      endColumn: s.col + 1,
    })
  }

  // Unterminated string
  if (stringDelim !== null) {
    errors.push({
      message: 'Unterminated string literal',
      line: lines.length,
      column: 1,
      endLine: lines.length,
      endColumn: 1,
    })
  }

  // Unterminated block comment
  if (inBlockComment) {
    errors.push({
      message: 'Unterminated block comment',
      line: lines.length,
      column: 1,
      endLine: lines.length,
      endColumn: 1,
    })
  }

  return errors
}

function checkCpp(code: string): SyntaxError[] {
  const errors: SyntaxError[] = []
  const lines = code.split('\n')

  for (let i = 0; i < lines.length; i++) {
    const trimmed = lines[i].trimEnd()
    if (trimmed === '' || trimmed.startsWith('//') || trimmed.startsWith('#')) continue
    const bare = trimmed.replace(/\/\/.*$/, '').trim()
    if (bare === '' || bare === '{' || bare === '}' || bare === '{}') continue

    // return statement without semicolon
    if (/^return\s+/.test(bare) && !bare.endsWith(';') && !bare.endsWith('}')) {
      errors.push({
        message: "Missing semicolon ';'",
        line: i + 1,
        column: trimmed.length + 1,
        endLine: i + 1,
        endColumn: trimmed.length + 2,
      })
    }

    // "int x = 5" / "string s" / etc  without semicolon before a '{' or '}'
    if (/^(int|char|bool|float|double|long|string|auto|const|static|unsigned)\s+\w+\s*[^;]*$/.test(bare) &&
        !bare.endsWith(';') && !bare.endsWith('{') && !bare.endsWith('}')) {
      errors.push({
        message: "Missing semicolon ';'",
        line: i + 1,
        column: trimmed.length + 1,
        endLine: i + 1,
        endColumn: trimmed.length + 2,
      })
    }
  }
  return errors
}

function checkPython(code: string): SyntaxError[] {
  const errors: SyntaxError[] = []
  const lines = code.split('\n')

  // Indentation: flag mixed tabs and spaces
  let warnedMixed = false
  for (let i = 0; i < lines.length; i++) {
    if (!warnedMixed && lines[i].includes('\t') && /^ +/.test(lines[i])) {
      errors.push({
        message: 'Mixed tabs and spaces in indentation',
        line: i + 1,
        column: 1,
        endLine: i + 1,
        endColumn: 5,
      })
      warnedMixed = true
    }
  }

  // Missing colon after if/elif/else/while/for/def/class/try/except/finally/with
  const needsColon = /^\s*(if|elif|else|while|for|def|class|try|except|finally|with)\b/
  for (let i = 0; i < lines.length; i++) {
    const trimmed = lines[i].trimEnd()
    if (trimmed === '' || trimmed.startsWith('#')) continue
    const bare = trimmed.replace(/#.*$/, '').trim()
    if (bare === '') continue

    if (needsColon.test(bare) && !bare.endsWith(':') && !bare.endsWith('{')) {
      errors.push({
        message: "Missing colon ':'",
        line: i + 1,
        column: Math.max(1, lines[i].length),
        endLine: i + 1,
        endColumn: Math.max(2, lines[i].length + 1),
      })
    }
  }

  return errors
}

export function useSyntaxChecker() {
  const owner = 'judgex-syntax-check'

  function check(editor: monaco.editor.IStandaloneCodeEditor, language: string) {
    const model = editor.getModel()
    if (!model) return

    const code = model.getValue()
    const all: SyntaxError[] = [...checkCommon(code)]

    switch (language) {
      case 'cpp':
      case 'c':
      case 'java':
        all.push(...checkCpp(code))
        break
      case 'python':
        all.push(...checkPython(code))
        break
    }

    monaco.editor.setModelMarkers(model, owner, all.map(e => ({
      message: e.message,
      severity: monaco.MarkerSeverity.Error,
      startLineNumber: e.line,
      startColumn: e.column,
      endLineNumber: e.endLine,
      endColumn: e.endColumn,
    })))
  }

  function clear(model: monaco.editor.ITextModel) {
    monaco.editor.setModelMarkers(model, owner, [])
  }

  return { check, clear }
}
