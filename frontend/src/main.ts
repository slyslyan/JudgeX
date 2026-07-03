import './style.css'
import { createApp } from 'vue'
import App from './App.vue'
import router from './router'
import { vIconColor } from './directives/iconColor'

// Dark mode init
if (localStorage.getItem('theme') === 'dark' ||
    (!localStorage.getItem('theme') && window.matchMedia('(prefers-color-scheme: dark)').matches)) {
  document.documentElement.classList.add('dark')
}

const app = createApp(App)
app.use(router)
app.directive('icon-color', vIconColor)
app.mount('#app')
