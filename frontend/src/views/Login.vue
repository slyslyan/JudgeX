<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { login } from '../api'
import { Eye, EyeOff, Sparkles } from 'lucide-vue-next'

const router = useRouter()

const username = ref('')
const password = ref('')
const showPassword = ref(false)
const error = ref('')
const isLoading = ref(false)

const mouseX = ref(0)
const mouseY = ref(0)

function onMouseMove(e: MouseEvent) {
  mouseX.value = e.clientX
  mouseY.value = e.clientY
}

const purpleChar = ref<HTMLElement>()
const blackChar = ref<HTMLElement>()
const yellowChar = ref<HTMLElement>()
const orangeChar = ref<HTMLElement>()

const isTyping = ref(false)
const isPurpleBlinking = ref(false)
const isBlackBlinking = ref(false)
const isPurplePeeking = ref(false)
const isLookingAtEachOther = ref(false)

let purpleBlinkTimer: ReturnType<typeof setTimeout> | undefined
let blackBlinkTimer: ReturnType<typeof setTimeout> | undefined
let peekTimer: ReturnType<typeof setTimeout> | undefined

function schedulePurpleBlink() {
  purpleBlinkTimer = setTimeout(() => {
    isPurpleBlinking.value = true
    setTimeout(() => {
      isPurpleBlinking.value = false
      schedulePurpleBlink()
    }, 150)
  }, Math.random() * 4000 + 3000)
}

function scheduleBlackBlink() {
  blackBlinkTimer = setTimeout(() => {
    isBlackBlinking.value = true
    setTimeout(() => {
      isBlackBlinking.value = false
      scheduleBlackBlink()
    }, 150)
  }, Math.random() * 4000 + 3000)
}

function calcCharacterPos(ref: HTMLElement | undefined) {
  if (!ref) return { faceX: 0, faceY: 0, bodySkew: 0 }
  const rect = ref.getBoundingClientRect()
  const cx = rect.left + rect.width / 2
  const cy = rect.top + rect.height / 3
  const dx = mouseX.value - cx
  const dy = mouseY.value - cy
  return {
    faceX: Math.max(-15, Math.min(15, dx / 20)),
    faceY: Math.max(-10, Math.min(10, dy / 30)),
    bodySkew: Math.max(-6, Math.min(6, -dx / 120)),
  }
}

async function handleSubmit() {
  error.value = ''
  isLoading.value = true
  try {
    const res = await login(username.value, password.value)
    localStorage.setItem('token', res.data.token)
    localStorage.setItem('user', JSON.stringify({
      id: res.data.user_id,
      username: res.data.username,
      role: res.data.role,
    }))
    router.push('/')
  } catch (e: any) {
    error.value = e.response?.data?.error || '用户名或密码错误'
  } finally {
    isLoading.value = false
  }
}

onMounted(() => {
  window.addEventListener('mousemove', onMouseMove)
  schedulePurpleBlink()
  scheduleBlackBlink()
})

onUnmounted(() => {
  window.removeEventListener('mousemove', onMouseMove)
  clearTimeout(purpleBlinkTimer)
  clearTimeout(blackBlinkTimer)
  clearTimeout(peekTimer)
})

watch(isTyping, (v) => {
  if (v) {
    isLookingAtEachOther.value = true
    setTimeout(() => { isLookingAtEachOther.value = false }, 800)
  } else {
    isLookingAtEachOther.value = false
  }
})

watch([password, showPassword], () => {
  if (password.value.length > 0 && showPassword.value) {
    clearTimeout(peekTimer)
    const schedulePeek = () => {
      peekTimer = setTimeout(() => {
        isPurplePeeking.value = true
        setTimeout(() => { isPurplePeeking.value = false }, 800)
        schedulePeek()
      }, Math.random() * 3000 + 2000)
    }
    schedulePeek()
  } else {
    clearTimeout(peekTimer)
    isPurplePeeking.value = false
  }
})
</script>

<template>
  <div class="min-h-screen grid lg:grid-cols-2">
    <!-- Left: Characters (white) -->
    <div class="relative hidden lg:flex flex-col justify-between bg-zinc-50 p-12 text-zinc-950" style="background: #fafafa">
      <div class="relative z-20">
        <div class="flex items-center gap-2 text-lg font-semibold">
          <div class="size-8 rounded-lg bg-zinc-900/10 backdrop-blur-sm flex items-center justify-center">
            <Sparkles class="size-4" />
          </div>
          <span>JudgeX</span>
        </div>
      </div>

      <div class="relative z-20 flex items-end justify-center" style="height: 500px;">
        <div class="relative" style="width: 550px; height: 400px;">
          <!-- Purple -->
          <div
            ref="purpleChar"
            class="absolute bottom-0 transition-all duration-700 ease-in-out"
            :style="{
              left: '70px',
              width: '180px',
              height: isTyping || (password.length > 0 && !showPassword) ? '440px' : '400px',
              backgroundColor: '#6C3FF5',
              borderRadius: '10px 10px 0 0',
              zIndex: 1,
              transform:
                showPassword && password.length > 0
                  ? 'skewX(0deg)'
                  : isTyping || (password.length > 0 && !showPassword)
                    ? `skewX(${(calcCharacterPos(purpleChar).bodySkew || 0) - 12}deg) translateX(40px)`
                    : `skewX(${calcCharacterPos(purpleChar).bodySkew || 0}deg)`,
              transformOrigin: 'bottom center',
            }"
          >
            <div
              class="absolute flex gap-8 transition-all duration-700 ease-in-out"
              :style="{
                left: showPassword && password.length > 0 ? '20px' : isLookingAtEachOther ? '55px' : `${45 + calcCharacterPos(purpleChar).faceX}px`,
                top: showPassword && password.length > 0 ? '35px' : isLookingAtEachOther ? '65px' : `${40 + calcCharacterPos(purpleChar).faceY}px`,
              }"
            >
              <div v-for="k in 2" :key="k" class="rounded-full bg-white flex items-center justify-center transition-all duration-150 overflow-hidden"
                :style="{ width: '18px', height: isPurpleBlinking ? '2px' : '18px' }">
                <div v-if="!isPurpleBlinking" class="rounded-full bg-[#2D2D2D]"
                  :style="{
                    width: '7px', height: '7px',
                    transform: showPassword && password.length > 0
                      ? `translate(${isPurplePeeking ? 4 : -4}px, ${isPurplePeeking ? 5 : -4}px)`
                      : isLookingAtEachOther ? 'translate(3px, 4px)' : 'translate(0, 0)',
                    transition: 'transform 0.1s ease-out',
                  }" />
              </div>
            </div>
          </div>

          <!-- Black -->
          <div
            ref="blackChar"
            class="absolute bottom-0 transition-all duration-700 ease-in-out"
            :style="{
              left: '240px', width: '120px', height: '310px',
              backgroundColor: '#2D2D2D', borderRadius: '8px 8px 0 0', zIndex: 2,
              transform: showPassword && password.length > 0 ? 'skewX(0deg)'
                : isLookingAtEachOther ? `skewX(${(calcCharacterPos(blackChar).bodySkew || 0) * 1.5 + 10}deg) translateX(20px)`
                  : isTyping || (password.length > 0 && !showPassword) ? `skewX(${(calcCharacterPos(blackChar).bodySkew || 0) * 1.5}deg)`
                    : `skewX(${calcCharacterPos(blackChar).bodySkew || 0}deg)`,
              transformOrigin: 'bottom center',
            }"
          >
            <div class="absolute flex gap-6 transition-all duration-700 ease-in-out"
              :style="{
                left: showPassword && password.length > 0 ? '10px' : isLookingAtEachOther ? '32px' : `${26 + calcCharacterPos(blackChar).faceX}px`,
                top: showPassword && password.length > 0 ? '28px' : isLookingAtEachOther ? '12px' : `${32 + calcCharacterPos(blackChar).faceY}px`,
              }">
              <div v-for="k in 2" :key="k" class="rounded-full bg-white flex items-center justify-center transition-all duration-150 overflow-hidden"
                :style="{ width: '16px', height: isBlackBlinking ? '2px' : '16px' }">
                <div v-if="!isBlackBlinking" class="rounded-full bg-[#2D2D2D]"
                  :style="{
                    width: '6px', height: '6px',
                    transform: showPassword && password.length > 0 ? 'translate(-4px, -4px)' : isLookingAtEachOther ? 'translate(0, -4px)' : 'translate(0, 0)',
                    transition: 'transform 0.1s ease-out',
                  }" />
              </div>
            </div>
          </div>

          <!-- Orange -->
          <div
            ref="orangeChar"
            class="absolute bottom-0 transition-all duration-700 ease-in-out"
            :style="{
              left: '0px', width: '240px', height: '200px', zIndex: 3,
              backgroundColor: '#FF9B6B', borderRadius: '120px 120px 0 0',
              transform: showPassword && password.length > 0 ? 'skewX(0deg)' : `skewX(${calcCharacterPos(orangeChar).bodySkew || 0}deg)`,
              transformOrigin: 'bottom center',
            }"
          >
            <div class="absolute flex gap-8 transition-all duration-200 ease-out"
              :style="{
                left: showPassword && password.length > 0 ? '50px' : `${82 + (calcCharacterPos(orangeChar).faceX || 0)}px`,
                top: showPassword && password.length > 0 ? '85px' : `${90 + (calcCharacterPos(orangeChar).faceY || 0)}px`,
              }">
              <div v-for="k in 2" :key="k" class="rounded-full bg-[#2D2D2D]"
                :style="{
                  width: '12px', height: '12px',
                  transform: showPassword && password.length > 0 ? 'translate(-5px, -4px)' : 'translate(0, 0)',
                  transition: 'transform 0.1s ease-out',
                }" />
            </div>
          </div>

          <!-- Yellow -->
          <div
            ref="yellowChar"
            class="absolute bottom-0 transition-all duration-700 ease-in-out"
            :style="{
              left: '310px', width: '140px', height: '230px', zIndex: 4,
              backgroundColor: '#E8D754', borderRadius: '70px 70px 0 0',
              transform: showPassword && password.length > 0 ? 'skewX(0deg)' : `skewX(${calcCharacterPos(yellowChar).bodySkew || 0}deg)`,
              transformOrigin: 'bottom center',
            }"
          >
            <div class="absolute flex gap-6 transition-all duration-200 ease-out"
              :style="{
                left: showPassword && password.length > 0 ? '20px' : `${52 + (calcCharacterPos(yellowChar).faceX || 0)}px`,
                top: showPassword && password.length > 0 ? '35px' : `${40 + (calcCharacterPos(yellowChar).faceY || 0)}px`,
              }">
              <div v-for="k in 2" :key="k" class="rounded-full bg-[#2D2D2D]"
                :style="{
                  width: '12px', height: '12px',
                  transform: showPassword && password.length > 0 ? 'translate(-5px, -4px)' : 'translate(0, 0)',
                  transition: 'transform 0.1s ease-out',
                }" />
            </div>
            <div class="absolute h-[4px] rounded-full bg-[#2D2D2D] transition-all duration-200 ease-out"
              :style="{
                width: '80px',
                left: showPassword && password.length > 0 ? '10px' : `${40 + (calcCharacterPos(yellowChar).faceX || 0)}px`,
                top: showPassword && password.length > 0 ? '88px' : `${88 + (calcCharacterPos(yellowChar).faceY || 0)}px`,
              }" />
          </div>
        </div>
      </div>

      <div class="relative z-20 flex items-center gap-8 text-sm text-zinc-500">
        <span>JudgeX Online Judge</span>
      </div>
    </div>

    <!-- Right: Login form (dark) -->
    <div class="relative flex items-center justify-center p-8 bg-black">
      <div class="relative z-10 w-full max-w-[420px]">
        <div class="text-center mb-10">
          <h1 class="text-3xl font-bold tracking-tight text-zinc-50 mb-2">JudgeX</h1>
          <p class="text-zinc-400 text-sm">请登录你的账号</p>
        </div>

        <form @submit.prevent="handleSubmit" class="space-y-5">
          <div class="space-y-2">
            <label class="text-sm font-medium text-zinc-100">用户名</label>
            <input
              v-model="username"
              type="text"
              placeholder="输入用户名"
              autocomplete="off"
              required
              class="w-full h-12 rounded-xl border border-zinc-600/40 bg-zinc-700/50 px-4 text-sm text-zinc-50 placeholder:text-zinc-400 focus:border-zinc-50 outline-none transition-all"
              @focus="isTyping = true"
              @blur="isTyping = false"
            />
          </div>

          <div class="space-y-2">
            <label class="text-sm font-medium text-zinc-100">密码</label>
            <div class="relative">
              <input
                v-model="password"
                :type="showPassword ? 'text' : 'password'"
                placeholder="••••••••"
                required
                class="w-full h-12 rounded-xl border border-zinc-600/40 bg-zinc-700/50 px-4 pr-12 text-sm text-zinc-50 placeholder:text-zinc-400 focus:border-zinc-50 outline-none transition-all"
              />
              <button
                type="button"
                class="absolute right-3 top-1/2 -translate-y-1/2 text-zinc-400 hover:text-zinc-50 transition-colors"
                @click="showPassword = !showPassword"
              >
                <EyeOff v-if="showPassword" class="size-5" />
                <Eye v-else class="size-5" />
              </button>
            </div>
          </div>

          <p v-if="error" class="p-3 text-sm text-red-400 bg-red-950/40 border border-red-900/50 rounded-lg">{{ error }}</p>

          <button
            type="submit"
            :disabled="isLoading"
            class="w-full h-12 rounded-xl bg-white text-black font-semibold text-base transition-all hover:bg-zinc-100 disabled:opacity-50 disabled:cursor-not-allowed shadow-xs shadow-black/10"
            style="background: #fff"
          >
            {{ isLoading ? '登录中...' : '登录' }}
          </button>
        </form>

        <div class="text-center text-sm text-zinc-400 mt-8">
          还没有账号？
          <router-link to="/register" class="text-zinc-50 font-medium hover:underline">注册</router-link>
        </div>
      </div>
    </div>
  </div>
</template>
