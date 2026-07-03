<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import AppTitlebar from '@/components/AppTitlebar.vue'
import OperationApprovalNotice from '@/components/OperationApprovalNotice.vue'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import { useSettingsStore } from '@/stores/settings'

const route = useRoute()
const router = useRouter()
const settings = useSettingsStore()
const operationApprovalStore = useOperationApprovalStore()
const showWindowsTitlebar = computed(() => isWindowsShell() && route.path !== '/popover')
let approvalPollingStarted = false

function isWindowsShell() {
  return /win/i.test(window.navigator.platform)
}

function startApprovalPolling() {
  if (route.path === '/popover') return
  approvalPollingStarted = true
  void operationApprovalStore.loadPending(false)
  operationApprovalStore.startPolling()
}

onMounted(async () => {
  startApprovalPolling()
  await settings.loadAgentSettings()
  if (!settings.agentSettings.onboarding_completed && route.path !== '/onboarding' && route.path !== '/popover') {
    await router.replace('/onboarding')
  }
})

onBeforeUnmount(() => {
  if (approvalPollingStarted) operationApprovalStore.stopPolling()
})
</script>

<template>
  <div class="app-root" data-test="app-root">
    <AppTitlebar v-if="showWindowsTitlebar" />
    <div class="app-route-frame" data-test="app-route-frame">
      <RouterView />
    </div>
    <OperationApprovalNotice />
  </div>
</template>

<style scoped>
.app-root {
  display: flex;
  height: 100vh;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
  overflow: hidden;
  background: var(--bg-primary);
}

.app-route-frame {
  min-width: 0;
  min-height: 0;
  flex: 1;
  overflow: hidden;
}

.app-route-frame :deep(.main-layout),
.app-route-frame :deep(.settings-shell),
.app-route-frame :deep(.overview-page),
.app-route-frame :deep(.popover-root) {
  height: 100%;
}

.app-route-frame :deep(.onboarding-page),
.app-route-frame :deep(.onboarding-shell) {
  min-height: 100%;
}
</style>
