<!--
ProjectConfigEditor：项目配置编辑器外壳（配置唯一编辑入口）。

职责：
  - 持有项目配置草稿（深拷贝自 project），全程本地编辑
  - env 横向 tab 切换、增删改名
  - 双栏：左侧 ServiceRail 列表，右侧 ServiceCard 单服务配置
  - 保存：校验 → 拍平为 SetupPayload → PUT /setup → reloadProject → emit saved
  - 取消：丢弃草稿 → emit cancel
边界：
  - 不负责新建项目的落地（由父层在 saved 后处理 registry）
  - 删除运行中 service 的最终守卫在后端
-->
<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { api, type Project } from '@/api/agent'
import { useAgentStore } from '@/stores/agent'
import { projectToDraft, draftToPayload, validateDraft, type ConfigDraftService } from '@/lib/configDraft'
import EnvTabBar from './EnvTabBar.vue'
import ServiceRail from './ServiceRail.vue'
import ServiceCard from './ServiceCard.vue'

const props = defineProps<{ project: Project; isNew?: boolean }>()
const emit = defineEmits<{ saved: [Project]; cancel: [] }>()

const agentStore = useAgentStore()
const draft = ref(projectToDraft(props.project))
const activeEnv = ref('')
const activeServiceId = ref('')
const renamingEnv = ref('')
const hosts = ref<Array<{ id: string; name: string }>>([])
const errors = ref<string[]>([])
const saving = ref(false)
const saveError = ref<string | null>(null)

onMounted(async () => {
  const envs = draft.value.environments
  activeEnv.value = (envs.find(e => e.is_dev) ?? envs[0])?.name ?? ''
  // 默认选中第一个服务
  const first = draft.value.services[0]
  activeServiceId.value = first?.id || (draft.value.services.length > 0 ? '0' : '')
  try {
    const list = await api.listHosts()
    hosts.value = list.map(h => ({ id: h.id, name: h.name }))
  } catch {
    hosts.value = []
  }
})

const currentServices = computed(() => draft.value.services)

// activeService：优先按 id 匹配，id 为空时按索引字符串匹配
const activeService = computed<ConfigDraftService | null>(() => {
  const byId = draft.value.services.find(s => s.id && s.id === activeServiceId.value)
  if (byId) return byId
  const n = Number(activeServiceId.value)
  return isNaN(n) ? draft.value.services[0] ?? null : draft.value.services[n] ?? null
})

const activeServiceIndex = computed<number>(() => {
  const byId = draft.value.services.findIndex(s => s.id && s.id === activeServiceId.value)
  if (byId >= 0) return byId
  const n = Number(activeServiceId.value)
  return isNaN(n) ? 0 : n
})

function addEnv() {
  const base = 'env'
  let name = base
  let n = 1
  const taken = new Set(draft.value.environments.map(e => e.name))
  while (taken.has(name)) name = `${base}${n++}`
  draft.value.environments.push({ id: '', name, is_dev: false, order: draft.value.environments.length })
  activeEnv.value = name
  renamingEnv.value = name // 新增后立即进入改名态
}

function removeEnv(name: string) {
  draft.value.environments = draft.value.environments.filter(e => e.name !== name)
  for (const s of draft.value.services) {
    s.deployments = s.deployments.filter(d => d.env_name !== name)
  }
  if (activeEnv.value === name) {
    activeEnv.value = draft.value.environments[0]?.name ?? ''
  }
}

function renameEnv(oldName: string, newName: string) {
  const env = draft.value.environments.find(e => e.name === oldName)
  if (!env) return
  // 重名时拒绝
  if (draft.value.environments.some(e => e.name === newName)) return
  env.name = newName
  // 同步所有 deployment 的 env_name 引用，否则 deployment 和环境脱钩
  for (const s of draft.value.services) {
    for (const d of s.deployments) {
      if (d.env_name === oldName) d.env_name = newName
    }
  }
  if (activeEnv.value === oldName) activeEnv.value = newName
  renamingEnv.value = ''
}

function toggleDev(name: string) {
  const env = draft.value.environments.find(e => e.name === name)
  if (env) env.is_dev = !env.is_dev
}

function addService() {
  const newSvc: ConfigDraftService = { id: '', name: '', required: false, order: draft.value.services.length, deployments: [] }
  draft.value.services.push(newSvc)
  activeServiceId.value = String(draft.value.services.length - 1)
}

function updateService(i: number, svc: ConfigDraftService) {
  draft.value.services[i] = svc
}

function removeService(i: number) {
  draft.value.services.splice(i, 1)
  // 删除后选中前一个或第一个
  const next = draft.value.services[i] ?? draft.value.services[i - 1] ?? draft.value.services[0]
  if (next) {
    activeServiceId.value = next.id || String(draft.value.services.indexOf(next))
  } else {
    activeServiceId.value = ''
  }
}

async function save() {
  errors.value = validateDraft(draft.value)
  if (errors.value.length) return
  saving.value = true
  saveError.value = null
  try {
    const updated = await api.putProjectSetup(props.project.id, draftToPayload(draft.value))
    await agentStore.reloadProject(props.project.id)
    emit('saved', updated)
  } catch (e) {
    saveError.value = e instanceof Error ? e.message : '保存失败'
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="editor-backdrop" @click.self="emit('cancel')">
    <div class="editor-body">
      <div class="editor-title">配置项目 · {{ project.name }}</div>

      <ul v-if="errors.length" class="err-list">
        <li v-for="(e, i) in errors" :key="i">{{ e }}</li>
      </ul>
      <div v-if="saveError" class="err-list">{{ saveError }}</div>

      <EnvTabBar
        :environments="draft.environments"
        :active="activeEnv"
        :renamingEnv="renamingEnv"
        @update:active="activeEnv = $event"
        @add-env="addEnv"
        @remove-env="removeEnv"
        @rename-env="renameEnv"
        @toggle-dev="toggleDev"
        @start-rename="renamingEnv = $event"
      />

      <!-- 双栏：左侧服务列表，右侧当前服务配置 -->
      <div class="editor-columns">
        <div class="editor-left">
          <ServiceRail
            :services="currentServices"
            :activeId="activeServiceId"
            :envName="activeEnv"
            @select="activeServiceId = $event"
            @add="addService"
            @remove="removeService"
          />
        </div>
        <div class="editor-right">
          <template v-if="activeService">
            <ServiceCard
              data-test="service-card"
              :service="activeService"
              :env-name="activeEnv"
              :hosts="hosts"
              :project-path="project.root_path"
              @update:service="updateService(activeServiceIndex, $event)"
              @remove="removeService(activeServiceIndex)"
            />
          </template>
          <div v-else class="editor-empty">请在左侧新增服务</div>
        </div>
      </div>

      <div class="editor-actions">
        <button type="button" data-test="config-cancel" @click="emit('cancel')">取消</button>
        <button type="button" class="primary" data-test="config-save" :disabled="saving" @click="save">
          {{ saving ? '保存中...' : '保存' }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.editor-backdrop {
  position: fixed;
  inset: 0;
  z-index: 100;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.45);
}
.editor-body {
  width: min(820px, calc(100vw - 32px));
  max-height: 88vh;
  overflow-y: auto;
  padding: 20px 22px;
  background: var(--bg-primary);
  border: 1px solid var(--border-secondary);
}
.editor-title {
  margin-bottom: 14px;
  font-size: 14px;
  font-weight: 600;
}
.err-list {
  margin: 0 0 12px;
  padding: 8px 12px;
  list-style: none;
  background: var(--bg-secondary);
  border-left: 2px solid var(--status-failed);
  color: var(--status-failed);
  font-size: 12px;
}
.editor-columns {
  display: grid;
  grid-template-columns: 180px 1fr;
  gap: 16px;
  border-top: 1px solid var(--border-secondary);
  padding-top: 12px;
  min-height: 320px;
}
.editor-left {
  border-right: 1px solid var(--border-secondary);
  padding-right: 12px;
}
.editor-right {
  min-width: 0;
}
.editor-empty {
  color: var(--text-tertiary);
  font-size: 12px;
  padding: 20px 0;
}
.editor-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px solid var(--border-secondary);
}
.editor-actions button {
  padding: 5px 14px;
  font-size: 12px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  cursor: pointer;
}
.editor-actions button.primary {
  color: #fff;
  background: var(--accent);
  border-color: var(--accent);
}
.editor-actions button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
