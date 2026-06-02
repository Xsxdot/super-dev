import { createRouter, createWebHashHistory } from 'vue-router'
import MainPage from '@/pages/MainPage.vue'

const PopoverPage = () => import('@/pages/PopoverPage.vue')
const SettingsPage = () => import('@/pages/SettingsPage.vue')
const ProjectOverviewPage = () => import('@/pages/ProjectOverviewPage.vue')
const RunConsolePage = () => import('@/components/Overview/RunConsole/RunConsolePage.vue')

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', component: MainPage },
    { path: '/popover', component: PopoverPage },
    { path: '/settings', component: SettingsPage },
    { path: '/project/:id/overview', component: ProjectOverviewPage },
    { path: '/project/:id/pipelines/:pipelineId/runs/:runId', component: RunConsolePage },
  ],
})

export default router
