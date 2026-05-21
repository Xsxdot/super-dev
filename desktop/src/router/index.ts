import { createRouter, createWebHashHistory } from 'vue-router'
import MainPage from '@/pages/MainPage.vue'

const PopoverPage = () => import('@/pages/PopoverPage.vue')
const SettingsPage = () => import('@/pages/SettingsPage.vue')

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', component: MainPage },
    { path: '/popover', component: PopoverPage },
    { path: '/settings', component: SettingsPage },
  ],
})

export default router
