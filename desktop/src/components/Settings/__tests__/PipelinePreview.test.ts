/**
 * PipelinePreview 测试后端 DAG 预览结果的展示。
 *
 * 职责：
 *   - 验证步骤按 phase 展示
 *   - 验证步骤依赖和目标主机可见
 *
 * 边界：
 *   - 不触发真实预览请求
 *   - 不校验 DAG 正确性
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import PipelinePreview from '@/components/Settings/PipelinePreview.vue'
import type { PipelinePreviewResponse } from '@/api/agent'

describe('PipelinePreview', () => {
  it('按 phase 展示步骤和依赖', () => {
    const preview: PipelinePreviewResponse = {
      run: {
        deployment_id: 'd1',
        status: 'pending',
        step_runs: [
          { step_name: 'Build', type: 'local_command', phase: 'build', status: 'pending', tasks: [{ status: 'pending' }] },
          { step_name: 'Deploy.Upload', type: 'transfer', phase: 'deploy', needs: ['Build'], status: 'pending', tasks: [{ host_id: 'h1', host_name: 'box1', status: 'pending' }] },
        ],
      },
    }
    const wrapper = mount(PipelinePreview, { props: { preview } })

    expect(wrapper.text()).toContain('build')
    expect(wrapper.text()).toContain('Build')
    expect(wrapper.text()).toContain('Deploy.Upload')
    expect(wrapper.text()).toContain('box1')
  })
})
