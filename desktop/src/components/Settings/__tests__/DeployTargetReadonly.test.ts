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
  it('shows pipeline-level hosts per environment without service rows', () => {
    const wrapper = mount(DeployTargetReadonly, {
      props: {
        targetsByEnv: {
          dev: ['local-01', 'local-02'],
          test: ['test-01', 'test-02'],
          prod: ['ali-01', 'ali-02', 'ali-03', 'ali-04'],
        },
      },
      global: { plugins: [installTestI18n()] },
    })
    expect(wrapper.get('[data-test="deploy-target-label"]').text()).toContain('当前流水线')
    expect(wrapper.get('[data-test="deploy-target-env-dev"]').text()).toContain('local-01')
    expect(wrapper.get('[data-test="deploy-target-env-test"]').text()).toContain('test-01')
    expect(wrapper.get('[data-test="deploy-target-env-prod"]').text()).toContain('ali-01')
    expect(wrapper.get('[data-test="deploy-target-env-prod"]').text()).toContain('+2')
    expect(wrapper.get('[data-test="deploy-target-overflow-prod"]').text()).toContain('ali-04')
    expect(wrapper.text()).not.toContain('Service')
  })
})
