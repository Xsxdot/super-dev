<script setup lang="ts">
import { onBeforeUnmount, onMounted } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import OperationApprovalNotice from '@/components/OperationApprovalNotice.vue'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import { useSettingsStore } from '@/stores/settings'

const route = useRoute()
const router = useRouter()
const settings = useSettingsStore()
const operationApprovalStore = useOperationApprovalStore()
let approvalPollingStarted = false

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
  <RouterView />
  <OperationApprovalNotice />
</template>
