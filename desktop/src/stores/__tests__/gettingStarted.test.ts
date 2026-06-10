/**
 * gettingStarted store 测试起步旅程清单的本地状态与检测规则。
 *
 * 职责：
 *   - 验证完成步骤与 dismissed 标记的 localStorage 持久化
 *   - 验证 step0..step5 的完成检测纯函数
 *   - 验证进度、当前步骤和可见性派生状态
 *
 * 边界：
 *   - 不渲染侧边栏组件
 *   - 不调用真实 agent API
 */
import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import {
  COMPLETED_STEPS_KEY,
  DISMISSED_KEY,
  deriveDetection,
  isSampleProject,
  useGettingStartedStore,
} from '@/stores/gettingStarted'
import type { Project } from '@/api/agent'

function makeProject(overrides: Partial<Project>): Project {
  return {
    id: 'p1',
    name: 'demo',
    root_path: '/home/u/demo',
    services: [],
    ...overrides,
  } as Project
}

describe('gettingStarted store 持久化与只进不退', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('markCompleted 写入 localStorage 且去重', () => {
    const store = useGettingStartedStore()

    store.markCompleted('step2')
    store.markCompleted('step2')

    expect(store.completedSteps).toEqual(['step2'])
    expect(JSON.parse(localStorage.getItem(COMPLETED_STEPS_KEY) ?? '[]')).toEqual(['step2'])
  })

  it('已完成步骤只进不退：reconcile 传入 false 不移除已完成', () => {
    const store = useGettingStartedStore()

    store.markCompleted('step3')
    store.reconcile({ step0: false, step1: false, step2: false, step3: false, step4: false, step5: false })

    expect(store.completedSteps).toContain('step3')
  })

  it('reconcile 把新达成的步骤标记为完成', () => {
    const store = useGettingStartedStore()

    store.reconcile({ step0: true, step1: false, step2: true, step3: false, step4: false, step5: false })

    expect(store.completedSteps.sort()).toEqual(['step0', 'step2'])
  })

  it('dismiss 持久化且 visible 变 false', () => {
    const store = useGettingStartedStore()

    store.dismiss()

    expect(store.dismissed).toBe(true)
    expect(store.visible).toBe(false)
    expect(localStorage.getItem(DISMISSED_KEY)).toBe('true')
  })

  it('从 localStorage 恢复已完成与 dismissed', () => {
    localStorage.setItem(COMPLETED_STEPS_KEY, JSON.stringify(['step0', 'step1']))
    localStorage.setItem(DISMISSED_KEY, 'true')

    const store = useGettingStartedStore()

    expect(store.completedSteps.sort()).toEqual(['step0', 'step1'])
    expect(store.dismissed).toBe(true)
  })
})

describe('deriveDetection 检测规则', () => {
  it('进入主页后前两步都视为完成', () => {
    const detection = deriveDetection({
      onboardingCompleted: true,
      projects: [],
      sampleSeeded: false,
      nodes: [],
      step1ApprovedSample: false,
    })

    expect(detection.step0).toBe(true)
    expect(detection.step1).toBe(true)
  })

  it('step2：非 sample 项目有 local 部署时完成', () => {
    const projects = [makeProject({
      root_path: '/home/u/myapp',
      services: [{
        id: 's1',
        project_id: 'p1',
        name: 'api',
        status: '',
        required: false,
        order: 0,
        deployments: [{ id: 'd1', env_name: 'dev', location: 'local', status: '' }],
      }],
    })]

    const detection = deriveDetection({
      onboardingCompleted: false,
      projects,
      sampleSeeded: true,
      nodes: [],
      step1ApprovedSample: false,
    })

    expect(detection.step2).toBe(true)
    expect(detection.step4).toBe(false)
  })

  it('step4：有 remote 部署且 host_ids 非空时完成', () => {
    const projects = [makeProject({
      root_path: '/home/u/myapp',
      services: [{
        id: 's1',
        project_id: 'p1',
        name: 'api',
        status: '',
        required: false,
        order: 0,
        deployments: [{ id: 'd1', env_name: 'prod', location: 'remote', host_ids: ['h1'], status: '' }],
      }],
    })]

    const detection = deriveDetection({
      onboardingCompleted: false,
      projects,
      sampleSeeded: false,
      nodes: [],
      step1ApprovedSample: false,
    })

    expect(detection.step4).toBe(true)
  })

  it('step3：节点 reachable 且 agent healthy 时完成', () => {
    const detection = deriveDetection({
      onboardingCompleted: false,
      projects: [],
      sampleSeeded: false,
      step1ApprovedSample: false,
      nodes: [{
        host_id: 'h1',
        reachable: true,
        agent: { installed: true, health: 'healthy', reachable: true },
        deployments: [],
        updated_at: '',
      }],
    })

    expect(detection.step3).toBe(true)
  })

  it('step5：项目有 pipelines 时完成', () => {
    const projects = [makeProject({ root_path: '/home/u/myapp', pipelines: [{ id: 'pl1' } as never] })]

    const detection = deriveDetection({
      onboardingCompleted: false,
      projects,
      sampleSeeded: false,
      nodes: [],
      step1ApprovedSample: false,
    })

    expect(detection.step5).toBe(true)
  })

  it('isSampleProject：root_path 以 superdev-sample 结尾', () => {
    expect(isSampleProject(makeProject({ root_path: '/data/superdev-sample' }))).toBe(true)
    expect(isSampleProject(makeProject({ root_path: '/data/superdev-sample/' }))).toBe(true)
    expect(isSampleProject(makeProject({ root_path: '/home/u/myapp' }))).toBe(false)
  })

  it('sample 项目的 local 部署不算 step2', () => {
    const projects = [makeProject({
      root_path: '/data/superdev-sample',
      services: [{
        id: 's1',
        project_id: 'p1',
        name: 'sample-api',
        status: '',
        required: false,
        order: 0,
        deployments: [{ id: 'd1', env_name: 'demo', location: 'local', status: '' }],
      }],
    })]

    const detection = deriveDetection({
      onboardingCompleted: false,
      projects,
      sampleSeeded: true,
      nodes: [],
      step1ApprovedSample: false,
    })

    expect(detection.step2).toBe(false)
  })
})

describe('gettingStarted 派生 getter', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('progressText 反映已完成主线步骤数（共 5 个可勾步骤）', () => {
    const store = useGettingStartedStore()

    store.markCompleted('step0')
    store.markCompleted('step1')

    expect(store.completedCount).toBe(2)
    expect(store.totalSteps).toBe(5)
  })

  it('currentStep 是第一个未完成步骤', () => {
    const store = useGettingStartedStore()

    store.markCompleted('step0')

    expect(store.currentStep).toBe('step1')
  })

  it('全部 5 个可勾步骤完成后 allDone 为 true', () => {
    const store = useGettingStartedStore()

    ;(['step0', 'step1', 'step2', 'step3', 'step4'] as const).forEach(step => store.markCompleted(step))

    expect(store.allDone).toBe(true)
  })

  it('visible：未 dismiss 且未全完成时为 true', () => {
    const store = useGettingStartedStore()

    expect(store.visible).toBe(true)
    store.dismiss()

    expect(store.visible).toBe(false)
  })
})
