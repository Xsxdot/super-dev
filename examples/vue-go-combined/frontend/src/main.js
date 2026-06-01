/**
 * Vue frontend for the Vue + Go pipeline example.
 *
 * Responsibilities:
 *   - Render a minimal page served by the Go backend.
 *   - Fetch /api/info to prove API and static serving share one process.
 *
 * Boundaries:
 *   - Does not use a router or global store.
 *   - Does not require Nginx.
 */
import { createApp, h, onMounted, ref } from 'vue'

createApp({
  setup() {
    const info = ref({ app: 'loading', language: '', version: '' })
    onMounted(async () => {
      const response = await fetch('/api/info')
      info.value = await response.json()
    })
    return { info }
  },
  render() {
    return h('main', [
      h('h1', 'SuperDev Vue Go Example'),
      h('pre', JSON.stringify(this.info, null, 2)),
    ])
  },
}).mount('#app')
