/**
 * ServiceMatrixTable tests.
 *
 * Responsibilities:
 *   - Verify service matrix rows render aggregated primary environment health
 *   - Verify selecting a service is a row-level action
 *   - Verify a homed project's dev node bead tooltip is annotated with "@ <host>"
 *     (Task 12, pure presentation off Project.home_host_name)
 *
 * Boundaries:
 *   - Does not test runtime polling
 *   - Does not render selected service node detail
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import ServiceMatrixTable from '../ServiceMatrixTable.vue'
import { installTestI18n } from '@/test-utils/i18n'
import { buildServiceMatrix, type ServiceMatrix } from '@/lib/runtimeServiceMatrix'
import type { Project, RuntimeInstanceStatus } from '@/api/agent'

function matrix(): ServiceMatrix {
  return {
    environments: ['prod'],
    devEnvironments: ['dev'],
    preferredServiceId: 'svc-server',
    localDev: { healthy: 0, total: 1, instances: 1 },
    kpis: {
      critical: 1,
      services: 2,
      instances: 5,
      envs: [{ envName: 'prod', healthy: 4, total: 5 }],
    },
    rows: [
      {
        serviceId: 'svc-server',
        serviceName: 'server',
        total: 5,
        abnormal: 1,
        cpuPercent: 2.5,
        memBytes: 512 * 1024 * 1024,
        instances: [],
        nodeHealths: [
          { nodeId: 'n3', nodeName: 'node-03', envName: 'prod', health: 'stopped', isDev: false },
          { nodeId: 'n1', nodeName: 'node-01', envName: 'prod', health: 'running', isDev: false },
          { nodeId: 'n2', nodeName: 'node-02', envName: 'prod', health: 'running', isDev: false },
        ],
        envs: [
          { envName: 'prod', instances: [], total: 5, healthy: 4, abnormal: 1, debuggingCount: 0, health: 'stopped', label: 'Stopped 4/5' },
        ],
        devEnvs: [
          { envName: 'dev', instances: [], total: 1, healthy: 0, abnormal: 1, debuggingCount: 0, health: 'stopped', label: 'Stopped 0/1' },
        ],
      },
      {
        serviceId: 'svc-audio',
        serviceName: 'audio',
        total: 0,
        abnormal: 0,
        cpuPercent: null,
        memBytes: null,
        instances: [],
        nodeHealths: [],
        envs: [
          { envName: 'prod', instances: [], total: 0, healthy: 0, abnormal: 0, debuggingCount: 0, health: 'not_configured', label: 'Not configured' },
        ],
        devEnvs: [],
      },
    ],
  }
}

// --- 矩阵节点归属标注（Task 12，2026-08-03 fix round）---
//
// 之前的覆盖测试手工拼装了一个 ServiceMatrix：devEnvironments: ['dev'] 同时
// nodeHealths 里放一个 envName: 'dev' 的节点——这个组合 buildServiceMatrix()
// 从来不会真的产出（见 runtimeServiceMatrix.ts 的既有语义），测试因此是重言式，
// 掩盖了标注对任何真实数据都不生效的问题。这里改为直接喂真实 Project + 实例
// 给 buildServiceMatrix()，用它的真实输出去挂载组件，才能在标注回归时真的变红。
//
// dev-only 项目（唯一环境是 dev，没有 prod/staging）是唯一会让 dev 环境节点
// 真正出现在本组件 node-bead 里的场景：buildServiceMatrix() 对「不存在非 dev
// 环境」的项目会把 dev 环境本身回退为主矩阵列（primaryEnvironments），但
// devEnvironments（被隔离出主矩阵的 dev 环境名单）在这个回退分支恒为空数组
// ——所以判断「是不是 dev 节点」不能用 devEnvironments.includes(envName)，
// 必须用 buildServiceMatrix() 在构造时按 env.is_dev 显式写入的 nodeHealths[].isDev
// （见 runtimeServiceMatrix.ts 和 ServiceMatrixTable.vue 的对应改动）。
//
// 混合环境项目（同时有 prod/staging）里 dev 实例被隔离进 devEnvs，根本不会
// 出现在 nodeHealths / node-bead 里——那些行渲染在 ServiceDetailPane（不同
// 组件），标注这条能力在那里如何呈现是记录在案的后续跟进项，不在本任务范围。
function devOnlyProject(overrides: Partial<Project> = {}): Project {
  return {
    id: 'proj-1',
    name: 'TK',
    root_path: '/tmp/tk',
    environments: [{ id: 'env-dev', name: 'dev', is_dev: true, order: 1 }],
    services: [
      { id: 'svc-server', project_id: 'proj-1', name: 'server', status: '', required: true, order: 1, deployments: [] },
    ],
    ...overrides,
  }
}

function mixedProject(): Project {
  return {
    id: 'proj-2',
    name: 'Mixed',
    root_path: '/tmp/mixed',
    environments: [
      { id: 'env-dev', name: 'dev', is_dev: true, order: 1 },
      { id: 'env-prod', name: 'prod', is_dev: false, order: 2 },
    ],
    services: [
      { id: 'svc-server', project_id: 'proj-2', name: 'server', status: '', required: true, order: 1, deployments: [] },
    ],
  }
}

function devInstance(): RuntimeInstanceStatus {
  return {
    service_id: 'svc-server',
    service_name: 'server',
    env_name: 'dev',
    deployment_id: 'svc-server-dev-local',
    node_id: 'local',
    node_name: 'MacBook-Pro.local',
    is_local: true,
    metrics: { cpu_percent: 1, mem_bytes: 128 * 1024 * 1024, uptime_sec: 60, restarts: 0, health: 'running', base: 'command' },
  }
}

function prodInstance(): RuntimeInstanceStatus {
  return {
    service_id: 'svc-server',
    service_name: 'server',
    env_name: 'prod',
    deployment_id: 'svc-server-prod-n1',
    node_id: 'n1',
    node_name: 'node-01',
    is_local: false,
    metrics: { cpu_percent: 2, mem_bytes: 256 * 1024 * 1024, uptime_sec: 3600, restarts: 0, health: 'running', base: 'systemd' },
  }
}

describe('ServiceMatrixTable', () => {
  it('renders primary environment columns, service rows, node beads, and metrics', () => {
    const wrapper = mount(ServiceMatrixTable, {
      props: { matrix: matrix(), selectedServiceId: 'svc-server' },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.text()).toContain('Service Matrix')
    expect(wrapper.find('[data-test="matrix-env-prod"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="matrix-env-dev"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('server')
    expect(wrapper.text()).toContain('Stopped 4/5')
    expect(wrapper.text()).toContain('2.5%')
    expect(wrapper.text()).toContain('512 MiB')
    expect(wrapper.findAll('.node-bead')).toHaveLength(3)
  })

  it('emits select-service when a service row is clicked', async () => {
    const wrapper = mount(ServiceMatrixTable, {
      props: { matrix: matrix(), selectedServiceId: 'svc-server' },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="service-row-svc-audio"]').trigger('click')

    expect(wrapper.emitted('select-service')?.[0]).toEqual(['svc-audio'])
  })

  // 矩阵节点归属标注（Task 12）：全部基于真实 buildServiceMatrix() 输出，
  // 不再手工拼装不可能出现的 ServiceMatrix 组合。
  it('annotates a homed dev-only project\'s node bead tooltip with "@ <host>" using real buildServiceMatrix output', () => {
    const project = devOnlyProject({ home_host_name: 'dev-box' })
    const realMatrix = buildServiceMatrix(project, [devInstance()])
    // 前置断言：这就是导致标注失效的关键事实——dev-only 项目的 devEnvironments
    // 恒为空数组，即便它唯一的环境正是 dev 环境。任何依赖它做「是不是 dev 节点」
    // 判断的实现都会在这条真实数据上失效。
    expect(realMatrix.devEnvironments).toEqual([])
    expect(realMatrix.rows[0].nodeHealths).toHaveLength(1)

    const wrapper = mount(ServiceMatrixTable, {
      props: { matrix: realMatrix, selectedServiceId: realMatrix.preferredServiceId, homeHostName: project.home_host_name },
      global: { plugins: [installTestI18n('en-US')] },
    })

    const bead = wrapper.get('.node-bead')
    expect(bead.attributes('title')).toContain('@ dev-box')
  })

  it('does not annotate the node bead for a local (non-homed) dev-only project', () => {
    const project = devOnlyProject()
    const realMatrix = buildServiceMatrix(project, [devInstance()])

    const wrapper = mount(ServiceMatrixTable, {
      props: { matrix: realMatrix, selectedServiceId: realMatrix.preferredServiceId },
      global: { plugins: [installTestI18n('en-US')] },
    })

    const bead = wrapper.get('.node-bead')
    expect(bead.attributes('title')).not.toContain('@')
  })

  it('does not annotate a real non-dev node bead even when the project is homed remotely', () => {
    // 混合环境项目：prod 实例走 primaryServiceInstances，真实产出 nodeHealths，
    // 但这些节点不是 dev 节点，不应该被标注——即便项目确实归属远端开发机。
    const project = { ...mixedProject(), home_host_name: 'dev-box' }
    const realMatrix = buildServiceMatrix(project, [prodInstance()])
    expect(realMatrix.rows[0].nodeHealths).toHaveLength(1)

    const wrapper = mount(ServiceMatrixTable, {
      props: { matrix: realMatrix, selectedServiceId: realMatrix.preferredServiceId, homeHostName: project.home_host_name },
      global: { plugins: [installTestI18n('en-US')] },
    })

    const bead = wrapper.get('.node-bead')
    expect(bead.attributes('title')).not.toContain('@')
  })
})
