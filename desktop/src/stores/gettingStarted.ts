/**
 * gettingStarted 聚合 store 管理「起步旅程清单」的完成状态与可见性。
 *
 * 职责：
 *   - 维护 step0..step5 的完成状态，并保证已完成步骤只进不退
 *   - 将已完成步骤与 dismissed 标记持久化到 desktop localStorage
 *   - 提供从现有项目、节点和设置快照派生完成状态的纯函数
 *
 * 边界：
 *   - 不直接调用业务 API
 *   - 不写 agent settings
 *   - 不渲染任何 UI
 */
import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import type { NodeStatus, Project } from '@/api/agent'

export type GettingStartedStep = 'step0' | 'step1' | 'step2' | 'step3' | 'step4' | 'step5'

export const ALL_STEPS: GettingStartedStep[] = ['step0', 'step1', 'step2', 'step3', 'step4', 'step5']
export const PRIMARY_STEPS: GettingStartedStep[] = ['step0', 'step1', 'step2', 'step3', 'step4']

export const COMPLETED_STEPS_KEY = 'superdev.gettingStarted.completedSteps'
export const DISMISSED_KEY = 'superdev.gettingStarted.dismissed'

export type StepDetection = Record<GettingStartedStep, boolean>

// DetectionInput 是检测每步完成所需的现有数据快照。调用方负责从各 store 收集。
export interface DetectionInput {
  onboardingCompleted: boolean
  sampleSeeded: boolean
  projects: Project[]
  nodes: NodeStatus[]
  step1ApprovedSample: boolean
}

// isSampleProject 判定项目是否是首启落地的内置示例项目。
//
// 参数：
//   - project: 待判定项目
//
// 返回：
//   - true 表示 root_path 去掉尾部斜杠后以 superdev-sample 结尾
export function isSampleProject(project: Project): boolean {
  return project.root_path.replace(/[\\/]+$/, '').endsWith('superdev-sample')
}

// deriveDetection 把现有 store 快照映射为起步旅程每步是否达成。
//
// 参数：
//   - input: 设置、项目、节点和异步审计结果快照
//
// 返回：
//   - step0..step5 的布尔检测结果
//
// 注意：
//   - 这是纯函数，不持久化、不触发副作用，便于单测覆盖规则。
export function deriveDetection(input: DetectionInput): StepDetection {
  const nonSampleProjects = input.projects.filter(project => !isSampleProject(project))
  const nonSampleDeployments = nonSampleProjects.flatMap(project =>
    (project.services ?? []).flatMap(service => service.deployments ?? []),
  )
  const hasLocalDeployment = nonSampleDeployments.some(deployment => deployment.location === 'local')
  const hasRemoteDeployment = nonSampleDeployments.some(deployment =>
    deployment.location === 'remote' && (deployment.host_ids?.length ?? 0) > 0,
  )
  const hasHealthyNode = input.nodes.some(node => node.reachable && node.agent?.health === 'healthy')
  const hasPipeline = nonSampleProjects.some(project => (project.pipelines?.length ?? 0) > 0)

  return {
    step0: input.onboardingCompleted,
    step1: input.onboardingCompleted || input.step1ApprovedSample,
    step2: nonSampleProjects.length > 0 && hasLocalDeployment,
    step3: hasHealthyNode,
    step4: hasRemoteDeployment,
    step5: hasPipeline,
  }
}

function loadCompleted(): GettingStartedStep[] {
  try {
    const raw = localStorage.getItem(COMPLETED_STEPS_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter((step): step is GettingStartedStep => ALL_STEPS.includes(step as GettingStartedStep))
  } catch {
    return []
  }
}

function saveCompleted(steps: GettingStartedStep[]) {
  localStorage.setItem(COMPLETED_STEPS_KEY, JSON.stringify(steps))
}

export const useGettingStartedStore = defineStore('gettingStarted', () => {
  const completedSteps = ref<GettingStartedStep[]>(loadCompleted())
  const dismissed = ref(localStorage.getItem(DISMISSED_KEY) === 'true')

  // markCompleted 幂等地标记单个步骤完成，并同步写入 localStorage。
  function markCompleted(step: GettingStartedStep) {
    if (completedSteps.value.includes(step)) return
    completedSteps.value = [...completedSteps.value, step]
    saveCompleted(completedSteps.value)
  }

  // reconcile 将检测结果中的 true 步骤并入已完成集合；false 不会移除历史完成状态。
  function reconcile(detection: StepDetection) {
    for (const step of ALL_STEPS) {
      if (detection[step]) markCompleted(step)
    }
  }

  // dismiss 持久化关闭状态，让用户主动关闭后不再显示入口。
  function dismiss() {
    dismissed.value = true
    localStorage.setItem(DISMISSED_KEY, 'true')
  }

  // reopen 清除关闭状态，用于设置页重新打开起步引导。
  function reopen() {
    dismissed.value = false
    localStorage.setItem(DISMISSED_KEY, 'false')
  }

  // isStepCompleted 查询指定步骤是否已经完成。
  function isStepCompleted(step: GettingStartedStep): boolean {
    return completedSteps.value.includes(step)
  }

  const completedCount = computed(() =>
    PRIMARY_STEPS.filter(step => completedSteps.value.includes(step)).length,
  )
  const totalSteps = computed(() => PRIMARY_STEPS.length)
  const allDone = computed(() => completedCount.value === totalSteps.value)
  const currentStep = computed<GettingStartedStep | null>(
    () => PRIMARY_STEPS.find(step => !completedSteps.value.includes(step)) ?? null,
  )
  const visible = computed(() => !dismissed.value && !allDone.value)

  return {
    completedSteps,
    dismissed,
    completedCount,
    totalSteps,
    allDone,
    currentStep,
    visible,
    markCompleted,
    reconcile,
    dismiss,
    reopen,
    isStepCompleted,
  }
})
