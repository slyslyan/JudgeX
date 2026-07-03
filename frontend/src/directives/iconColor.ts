import type { Directive } from 'vue'

export const iconPalette = [
  '#3B82F6', // blue-500
  '#EF4444', // red-500
  '#10B981', // emerald-500
  '#F59E0B', // amber-500
  '#8B5CF6', // violet-500
  '#EC4899', // pink-500
  '#06B6D4', // cyan-500
  '#F97316', // orange-500
  '#14B8A6', // teal-500
  '#E11D48', // rose-500
]

export const vIconColor: Directive<HTMLElement, void> = {
  mounted(el) {
    el.querySelectorAll<HTMLElement>('svg:not([data-ic])').forEach((svg, i) => {
      svg.style.color = iconPalette[i % iconPalette.length]
      svg.setAttribute('data-ic', '')
    })
  },
  updated(el) {
    el.querySelectorAll<HTMLElement>('svg:not([data-ic])').forEach((svg, i) => {
      svg.style.color = iconPalette[i % iconPalette.length]
      svg.setAttribute('data-ic', '')
    })
  },
}
