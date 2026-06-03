# JudgeX 前端

Vue 3 + TypeScript + Tailwind CSS v4 + Monaco Editor

## 开发

```bash
npm install
npx vite --host
```

前端运行在 `http://localhost:5173`，API 自动代理到 `http://localhost:8080`。

## 构建

```bash
npx vite build
```

产物输出到 `dist/`。

## 技术栈

| 依赖 | 版本 |
|------|------|
| Vue | 3.5 |
| TypeScript | 6.0 |
| Tailwind CSS | 4.3 |
| Monaco Editor | 0.55 |
| Vue Router | 4.6 |
| Axios | 1.16 |

## 设计

Apple 极简风格，全中文界面，分层炭灰深色模式。自定义组件：
- `MonacoEditor.vue` — 代码编辑器封装（实时语法检查）
- `AiChat.vue` — AI 对话组件（SSE 流式）
- `MarkdownRenderer.vue` — Markdown 渲染
- `Navbar.vue` — 毛玻璃导航栏
- `AdminLayout.vue` — 管理后台左侧栏布局
