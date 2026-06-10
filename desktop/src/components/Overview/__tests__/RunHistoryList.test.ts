/**
 * RunHistoryList 测试单条流水线的最近运行历史。
 *
 * 职责：
 *   - 验证历史行和时间线节点数量一致
 *   - 验证失败摘要保留完整 title 但视觉结构可截断
 *   - 验证运行中记录不可回滚
 *
 * 边界：
 *   - 不验证浏览器像素布局
 *   - 不调用真实运行日志或回滚接口
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import RunHistoryList from '@/components/Overview/RunHistoryList.vue'
import runHistoryListSource from '../RunHistoryList.vue?raw'
import type { Run } from '@/api/agent'
import { installTestI18n } from '@/test-utils/i18n'

function run(partial: Partial<Run> = {}): Run {
  return {
    id: 'run-1',
    project_id: 'p1',
    pipeline_id: 'deploy-prod',
    env_name: 'prod',
    deployment_id: '',
    artifact_version: '20260609120345',
    status: 'success',
    step_runs: [],
    started_at: 1_780_998_225,
    finished_at: 1_780_998_268,
    ...partial,
  }
}

describe('RunHistoryList', () => {
  it('keeps timeline nodes matched to the visible history rows', () => {
    const wrapper = mount(RunHistoryList, {
      props: {
        runs: Array.from({ length: 7 }, (_, index) => run({ id: `run-${index}`, artifact_version: `v${index}` })),
        limit: 5,
      },
      global: { plugins: [installTestI18n()] },
    })

    expect(wrapper.findAll('[data-test="run-history-row"]')).toHaveLength(5)
    expect(wrapper.findAll('[data-test="run-history-node"]')).toHaveLength(5)
    expect(wrapper.find('[data-test="run-history"]').attributes('style')).toContain('--history-row-count: 5')
  })

  it('keeps failed summaries single-line addressable through title text', () => {
    const wrapper = mount(RunHistoryList, {
      props: {
        runs: [run({
          status: 'failed',
          step_runs: [
            { step_name: 'Health Check failed on ali-01', type: 'include', phase: 'deploy', status: 'failed', tasks: [] },
          ],
        })],
      },
      global: { plugins: [installTestI18n()] },
    })

    const summary = wrapper.find('[data-test="run-failed-summary"]')
    expect(summary.classes()).toContain('failed')
    expect(summary.attributes('title')).toContain('Health Check failed on ali-01')
  })

  it('disables rollback while a run is still running', () => {
    const wrapper = mount(RunHistoryList, {
      props: { runs: [run({ status: 'running', finished_at: 0 })] },
      global: { plugins: [installTestI18n()] },
    })

    expect(wrapper.find('[data-test="run-rollback"]').attributes('disabled')).toBeDefined()
  })

  it('removes the console action while keeping logs and rollback actions', () => {
    const wrapper = mount(RunHistoryList, {
      props: { runs: [run()] },
      global: { plugins: [installTestI18n()] },
    })

    expect(wrapper.find('[data-test="run-detail"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="run-log"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="run-rollback"]').exists()).toBe(true)
  })

  it('keeps the artifact kind column wide enough for the full header label', () => {
    expect(runHistoryListSource).toContain('--history-artifact-kind-width: 104px;')
    expect(runHistoryListSource).toContain('--history-actions-width: 260px;')
    expect(runHistoryListSource).toMatch(/grid-template-columns:\s*94px var\(--history-version-width\) 64px var\(--history-started-at-width\) 72px var\(--history-artifact-kind-width\) minmax\(150px,\s*1fr\) var\(--history-actions-width\);/)
  })

  it('keeps the version and start time columns wide enough for timestamp-like values', () => {
    expect(runHistoryListSource).toContain('--history-version-width: 176px;')
    expect(runHistoryListSource).toContain('--history-started-at-width: 184px;')
  })

  it('keeps history action buttons readable without bold squeezed text', () => {
    expect(runHistoryListSource).toMatch(/\.run-actions button\s*{[^}]*padding:\s*0 10px;[^}]*font-size:\s*13px;[^}]*font-weight:\s*500;/s)
  })

  it('shows promote action for successful run', () => {
    const wrapper = mount(RunHistoryList, {
      props: {
        runs: [run({ id: 'r1', env_name: 'test', artifact_version: 'v1', status: 'success' })],
        promotableEnvs: ['prod'],
      },
      global: { plugins: [installTestI18n()] },
    })
    expect(wrapper.find('[data-test="promote-r1-prod"]').exists()).toBe(true)
  })

  it('emits promote with artifact version and target env', async () => {
    const wrapper = mount(RunHistoryList, {
      props: {
        runs: [run({ id: 'r1', env_name: 'test', artifact_version: 'v1', status: 'success' })],
        promotableEnvs: ['prod'],
      },
      global: { plugins: [installTestI18n()] },
    })
    await wrapper.get('[data-test="promote-r1-prod"]').trigger('click')
    expect(wrapper.emitted('promote')?.[0][0]).toMatchObject({ runId: 'r1', artifactVersion: 'v1', targetEnv: 'prod' })
  })

  it('hides promote for failed run', () => {
    const wrapper = mount(RunHistoryList, {
      props: {
        runs: [run({ id: 'r2', env_name: 'test', artifact_version: 'v1', status: 'failed' })],
        promotableEnvs: ['prod'],
      },
      global: { plugins: [installTestI18n()] },
    })
    expect(wrapper.find('[data-test="promote-r2-prod"]').exists()).toBe(false)
  })
})
