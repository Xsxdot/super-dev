/**
 * DeployTargetReadonly 测试按环境展示部署目标。
 *
 * 职责：
 *   - 验证每个环境的部署目标主机渲染
 *
 * 边界：
 *   - 不编辑目标主机
 *   - 不从项目配置解析目标
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import DeployTargetReadonly from '../DeployTargetReadonly.vue'
import { installTestI18n } from '@/test-utils/i18n'

describe('DeployTargetReadonly', () => {
  it('shows hosts per environment', () => {
    const wrapper = mount(DeployTargetReadonly, {
      props: {
        targetsByEnv: { test: ['web-test-01', 'web-test-02'], prod: ['web-prod-01'] },
      },
      global: { plugins: [installTestI18n()] },
    })
    expect(wrapper.get('[data-test="deploy-target-test"]').text()).toContain('web-test-01')
    expect(wrapper.get('[data-test="deploy-target-prod"]').text()).toContain('web-prod-01')
  })
})
