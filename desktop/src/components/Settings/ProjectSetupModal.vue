<!--
ProjectSetupModal：项目 Environment 配置引导弹窗。

职责：
  - 步骤 1：提供「从头创建」和「从 launch.json 导入」两个路径入口
  - 步骤 2a（从头创建）：填写 env 名称 + 每个 service 的命令和工作目录
  - 步骤 2b（从 launch.json 导入）：展示 launch 配置列表，自动匹配 service，支持多选
  - 确认后调用 setup API，成功 emit done

边界：
  - 不修改 service 的 Name/Required/Order，只写 Deployments
  - 不自动关闭，由父组件监听 done 事件后处理
-->
<script setup lang="ts">
import { ref, watch } from 'vue'
import { api, type Project, type Service, type LaunchConfig, type SetupPayload } from '@/api/agent'
import { useAgentStore } from '@/stores/agent'

const props = defineProps<{
  visible: boolean
  project: Project
}>()

const emit = defineEmits<{
  done: []
  cancel: []
}>()

const agentStore = useAgentStore()

// 当前步骤：choose → scratch 或 launch
type Step = 'choose' | 'scratch' | 'launch'
const step = ref<Step>('choose')

// 公共 env 字段
const envName = ref('dev')
const envIsDev = ref(true)

// scratch 路径：每个 service 的 command 和 work_dir
interface ScratchRow {
  service: Service
  command: string
  work_dir: string
}
const scratchRows = ref<ScratchRow[]>([])

// launch 路径：launch 配置列表 + 选中状态
const launchConfigs = ref<LaunchConfig[]>([])
const launchSelected = ref<Set<string>>(new Set())

// 加载/错误状态
const loading = ref(false)
const error = ref<string | null>(null)

// 每次弹窗打开时重置所有状态，避免上次操作的残留数据
watch(() => props.visible, (val) => {
  if (!val) return
  step.value = 'choose'
  error.value = null
  envName.value = 'dev'
  envIsDev.value = true
  scratchRows.value = []
  launchConfigs.value = []
  launchSelected.value = new Set()
})

/**
 * matchLaunchToService 根据 launch 配置名匹配对应 service。
 *
 * 参数：
 *   - launchName: launch 配置名称
 *   - services: 当前项目的 service 列表
 *
 * 返回：
 *   - 匹配到的 service，未找到返回 undefined
 */
function matchLaunchToService(launchName: string, services: Service[]): Service | undefined {
  const lower = launchName.toLowerCase()
  return services.find(s => {
    const sl = s.name.toLowerCase()
    return sl === lower || lower.includes(sl) || sl.includes(lower)
  })
}

/**
 * initScratch 初始化从头创建路径，预填各 service 的默认命令和工作目录。
 */
function initScratch() {
  scratchRows.value = props.project.services.map(svc => ({
    service: svc,
    command: svc.command ?? '',
    work_dir: svc.work_dir ?? '',
  }))
  step.value = 'scratch'
}

/**
 * initLaunch 初始化从 launch.json 导入路径，调用 API 读取配置列表并自动匹配。
 */
async function initLaunch() {
  loading.value = true
  error.value = null
  try {
    const configs = await api.getVscodeLaunch(props.project.id)
    launchConfigs.value = configs
    // 自动勾选与 service 名称匹配的 launch 配置
    const autoSelected = new Set<string>()
    for (const cfg of configs) {
      if (matchLaunchToService(cfg.name, props.project.services)) {
        autoSelected.add(cfg.name)
      }
    }
    launchSelected.value = autoSelected
    step.value = 'launch'
  } catch (err) {
    error.value = err instanceof Error ? err.message : '读取 launch.json 失败'
  } finally {
    loading.value = false
  }
}

/**
 * toggleLaunch 切换指定 launch 配置的选中状态。
 *
 * 参数：
 *   - name: launch 配置名称
 */
function toggleLaunch(name: string) {
  const next = new Set(launchSelected.value)
  if (next.has(name)) next.delete(name)
  else next.add(name)
  launchSelected.value = next
}

/**
 * confirm 构建 SetupPayload，调用 API 完成项目配置，成功后 emit done。
 */
async function confirm() {
  loading.value = true
  error.value = null
  try {
    let payload: SetupPayload

    if (step.value === 'scratch') {
      // 从头创建：每个 service 使用填写的 command 和 work_dir
      payload = {
        environments: [{ name: envName.value, is_dev: envIsDev.value, order: 0 }],
        services: scratchRows.value.map(row => ({
          id: row.service.id,
          deployments: [
            {
              env_name: envName.value,
              location: 'local' as const,
              command: row.command,
              work_dir: row.work_dir,
            },
          ],
        })),
      }
    } else {
      // 从 launch.json 导入：每个 service 找匹配且选中的 launch 配置
      payload = {
        environments: [{ name: envName.value, is_dev: envIsDev.value, order: 0 }],
        services: props.project.services.map(svc => {
          // 在所有选中的 launch 配置中找能匹配这个 service 的
          const matchedLaunch = launchConfigs.value.find(cfg => {
            if (!launchSelected.value.has(cfg.name)) return false
            return matchLaunchToService(cfg.name, [svc]) !== undefined
          })
          return {
            id: svc.id,
            deployments: matchedLaunch
              ? [
                  {
                    env_name: envName.value,
                    location: 'local' as const,
                    command: matchedLaunch.command,
                    work_dir: matchedLaunch.work_dir,
                    env: matchedLaunch.env,
                  },
                ]
              : [],
          }
        }),
      }
    }

    await api.putProjectSetup(props.project.id, payload)
    await agentStore.reloadProject(props.project.id)
    emit('done')
  } catch (err) {
    error.value = err instanceof Error ? err.message : '配置失败'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div v-if="visible" class="modal-backdrop" @click.self="emit('cancel')">
    <div class="modal-body">
      <!-- 标题 -->
      <div class="modal-title">配置项目环境 · {{ project.name }}</div>

      <!-- 错误提示 -->
      <div v-if="error" class="state err">{{ error }}</div>

      <!-- 步骤 1：选择路径 -->
      <template v-if="step === 'choose'">
        <div class="choose-desc">请选择初始化方式：</div>
        <div class="choose-actions">
          <button
            type="button"
            data-test="setup-from-scratch"
            class="choose-btn"
            @click="initScratch"
          >
            <div class="choose-btn-title">从头创建</div>
            <div class="choose-btn-desc">手动填写每个服务的命令和工作目录</div>
          </button>
          <button
            type="button"
            data-test="setup-from-launch"
            class="choose-btn"
            :disabled="loading"
            @click="initLaunch"
          >
            <div class="choose-btn-title">从 launch.json 导入</div>
            <div class="choose-btn-desc">读取 .vscode/launch.json，自动匹配服务配置</div>
          </button>
        </div>
        <div v-if="loading" class="state">读取中...</div>
      </template>

      <!-- 步骤 2a：从头创建 -->
      <template v-else-if="step === 'scratch'">
        <div class="env-row">
          <label class="field-label">环境名称</label>
          <input v-model="envName" class="field-input" type="text" placeholder="dev" />
          <label class="field-label dev-label">
            <input v-model="envIsDev" type="checkbox" />
            开发环境
          </label>
        </div>
        <div class="service-list">
          <div
            v-for="row in scratchRows"
            :key="row.service.id"
            class="service-row"
            data-test="setup-service-row"
          >
            <span class="svc-name">{{ row.service.name }}</span>
            <input
              v-model="row.command"
              class="field-input"
              type="text"
              placeholder="启动命令"
            />
            <input
              v-model="row.work_dir"
              class="field-input"
              type="text"
              placeholder="工作目录"
            />
          </div>
        </div>
        <div class="actions">
          <button type="button" @click="step = 'choose'">← 返回</button>
          <button
            type="button"
            class="primary"
            data-test="setup-confirm"
            :disabled="loading"
            @click="confirm"
          >
            {{ loading ? '保存中...' : '确认' }}
          </button>
        </div>
      </template>

      <!-- 步骤 2b：从 launch.json 导入 -->
      <template v-else-if="step === 'launch'">
        <div v-if="launchConfigs.length === 0" class="state">未找到 launch 配置</div>
        <ul v-else class="launch-list">
          <li
            v-for="cfg in launchConfigs"
            :key="cfg.name"
            :class="{ selected: launchSelected.has(cfg.name) }"
            class="launch-row"
            data-test="launch-import-row"
            @click="toggleLaunch(cfg.name)"
          >
            <input
              type="checkbox"
              :checked="launchSelected.has(cfg.name)"
              @click.stop="toggleLaunch(cfg.name)"
            />
            <span class="launch-name">{{ cfg.name }}</span>
            <span class="launch-cmd">{{ cfg.command }}</span>
          </li>
        </ul>
        <div class="actions">
          <button type="button" @click="step = 'choose'">← 返回</button>
          <button
            type="button"
            class="primary"
            data-test="setup-confirm"
            :disabled="loading"
            @click="confirm"
          >
            {{ loading ? '保存中...' : '确认' }}
          </button>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 100;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.45);
}
.modal-body {
  width: min(560px, calc(100vw - 32px));
  max-height: 85vh;
  overflow-y: auto;
  padding: 20px 22px;
  background: var(--bg-primary);
  border: 1px solid var(--border-secondary);
}
.modal-title {
  margin-bottom: 14px;
  font-size: 14px;
  font-weight: 600;
}
.state {
  padding: 16px;
  color: var(--text-tertiary);
  text-align: center;
  font-size: 12px;
}
.state.err {
  color: var(--status-failed);
  text-align: left;
  padding: 8px 0;
}
/* 步骤 1 */
.choose-desc {
  font-size: 12px;
  color: var(--text-secondary);
  margin-bottom: 12px;
}
.choose-actions {
  display: flex;
  gap: 12px;
  margin-bottom: 8px;
}
.choose-btn {
  flex: 1;
  padding: 14px 12px;
  text-align: left;
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  cursor: pointer;
}
.choose-btn:hover {
  border-color: var(--accent);
}
.choose-btn-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 4px;
}
.choose-btn-desc {
  font-size: 11px;
  color: var(--text-tertiary);
  line-height: 1.4;
}
/* 步骤 2a */
.env-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.field-label {
  font-size: 12px;
  color: var(--text-secondary);
  white-space: nowrap;
}
.dev-label {
  display: flex;
  align-items: center;
  gap: 4px;
}
.field-input {
  flex: 1;
  padding: 4px 8px;
  font-size: 12px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  color: var(--text-primary);
  outline: none;
}
.service-list {
  margin-bottom: 14px;
}
.service-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 0;
  border-bottom: 1px solid var(--border-secondary);
}
.svc-name {
  min-width: 80px;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-primary);
}
/* 步骤 2b */
.launch-list {
  max-height: 360px;
  padding: 0;
  margin: 0 0 14px;
  overflow-y: auto;
  list-style: none;
}
.launch-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 8px;
  border-bottom: 1px solid var(--border-secondary);
  cursor: pointer;
}
.launch-row.selected {
  background: var(--bg-secondary);
}
.launch-name {
  font-size: 12px;
  font-weight: 600;
}
.launch-cmd {
  font-size: 11px;
  color: var(--text-tertiary);
}
/* 公共 actions */
.actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 4px;
}
button {
  padding: 5px 12px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  cursor: pointer;
  font-size: 12px;
}
button.primary {
  color: #fff;
  background: var(--accent);
  border-color: var(--accent);
}
button:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
</style>
