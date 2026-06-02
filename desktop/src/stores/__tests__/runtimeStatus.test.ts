import { setActivePinia, createPinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
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
    vi.useFakeTimers()
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('refreshes immediately and then polls on interval', async () => {
    vi.mocked(api.getRuntimeStatus)
      .mockResolvedValueOnce(response('api'))
      .mockResolvedValueOnce(response('web'))
    const store = useRuntimeStatusStore()

    store.start('p1', 5000)
    await Promise.resolve()
    expect(store.statusByProject.p1.environments[0].instances[0].service_name).toBe('api')

    await vi.advanceTimersByTimeAsync(5000)
    expect(store.statusByProject.p1.environments[0].instances[0].service_name).toBe('web')
    expect(api.getRuntimeStatus).toHaveBeenCalledTimes(2)
  })

  it('stops polling and keeps the last successful data on transient error', async () => {
    vi.mocked(api.getRuntimeStatus)
      .mockResolvedValueOnce(response('api'))
      .mockRejectedValueOnce(new Error('network down'))
    const store = useRuntimeStatusStore()

    store.start('p1', 1000)
    await Promise.resolve()
    await vi.advanceTimersByTimeAsync(1000)

    expect(store.statusByProject.p1.environments[0].instances[0].service_name).toBe('api')
    expect(store.errorByProject.p1).toBe('network down')

    store.stop('p1')
    await vi.advanceTimersByTimeAsync(3000)
    expect(api.getRuntimeStatus).toHaveBeenCalledTimes(2)
  })
})
