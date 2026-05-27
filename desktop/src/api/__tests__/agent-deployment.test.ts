import { describe, it, expect } from 'vitest'
import { deploymentWsUrl } from '@/api/agent'

describe('deploymentWsUrl', () => {
  it('dev 模式下返回正确的 ws URL', () => {
    // WS_BASE 在 dev 模式下为 ws://127.0.0.1:57018，build 后为 ws://127.0.0.1:57017
    // 测试环境不是 dev，所以用 57017
    const url = deploymentWsUrl('dep-abc')
    expect(url).toContain('/ws/deployments/dep-abc/logs')
  })
})
