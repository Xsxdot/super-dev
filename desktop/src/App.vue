<script setup lang="ts">
import { onMounted } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import OperationApprovalNotice from '@/components/OperationApprovalNotice.vue'
import { useSettingsStore } from '@/stores/settings'

const route = useRoute()
const router = useRouter()
const settings = useSettingsStore()

onMounted(async () => {
  await settings.loadAgentSettings()
  if (!settings.agentSettings.onboarding_completed && route.path !== '/onboarding' && route.path !== '/popover') {
    await router.replace('/onboarding')
  }
})
</script>

<template>
  <RouterView />
  <OperationApprovalNotice />
</template>
