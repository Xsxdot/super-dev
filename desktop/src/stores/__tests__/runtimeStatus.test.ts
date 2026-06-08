import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api, type RuntimeStatusResponse } from '@/api/agent'
import { useRuntimeStatusStore } from '../runtimeStatus'

vi.mock('@/api/agent', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/agent')>()
  return {
    ...actual,
    api: {
      ...actual.api,
      getRuntimeStatus: vi.fn(),
    },
  }
})

function response(serviceName: string): RuntimeStatusResponse {
  return {
    environments: [{
      env_name: 'dev',
      instances: [{
        service_id: `svc-${serviceName}`,
        service_name: serviceName,
        env_name: 'dev',
        deployment_id: `dep-${serviceName}`,
        node_id: 'local',
        node_name: 'local',
        is_local: true,
        metrics: {
          cpu_percent: 10,
          mem_bytes: 1024,
          uptime_sec: 60,
          restarts: 0,
          health: 'running',
          base: 'process',
        },
      }],
    }],
  }
}

describe('runtimeStatus store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('refreshes a project on demand', async () => {
    vi.mocked(api.getRuntimeStatus).mockResolvedValueOnce(response('api'))
    const store = useRuntimeStatusStore()

    await store.refresh('p1')

    expect(store.statusByProject.p1.environments[0].instances[0].service_name).toBe('api')
    expect(store.errorByProject.p1).toBeNull()
  })

  it('keeps last successful data on transient error', async () => {
    vi.mocked(api.getRuntimeStatus)
      .mockResolvedValueOnce(response('api'))
      .mockRejectedValueOnce(new Error('network down'))
    const store = useRuntimeStatusStore()

    await store.refresh('p1')
    await store.refresh('p1')

    expect(store.statusByProject.p1.environments[0].instances[0].service_name).toBe('api')
    expect(store.errorByProject.p1).toBe('network down')
  })

  it('deduplicates overlapping refreshes', async () => {
    let release!: (value: RuntimeStatusResponse) => void
    vi.mocked(api.getRuntimeStatus).mockImplementationOnce(() => new Promise(resolve => {
      release = resolve
    }))
    const store = useRuntimeStatusStore()

    const first = store.refresh('p1')
    const second = store.refresh('p1')
    release(response('api'))
    await Promise.all([first, second])

    expect(api.getRuntimeStatus).toHaveBeenCalledTimes(1)
  })
})
