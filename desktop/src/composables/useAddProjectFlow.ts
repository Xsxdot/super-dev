/**
 * 桌面端添加项目流程。
 *
 * 职责：
 *   - 统一执行选择目录、注册项目、导入 VS Code launch 配置、打开新项目配置编辑器
 *   - 向调用方暴露新项目配置编辑器状态和打开/关闭方法
 *
 * 边界：
 *   - 不保存项目配置草稿，保存由 ProjectConfigEditor 负责
 *   - 不管理项目列表轮询和服务生命周期
 */
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ask, message, open } from '@tauri-apps/plugin-dialog'
import { api, type Project } from '@/api/agent'
import { useAgentStore } from '@/stores/agent'

// emptyProjectHasNoServices 判断新注册项目是否还没有运行配置。
function emptyProjectHasNoServices(project: Project): boolean {
  return !project.services || project.services.length === 0
}

/**
 * useAddProjectFlow 统一主界面和设置页的添加项目交互。
 *
 * 返回：
 *   - editorProject/editorIsNew: ProjectConfigEditor 的渲染状态
 *   - addProject: 打开目录选择器并初始化新项目配置草稿
 *   - closeEditor/onEditorSaved: 关闭编辑器的事件处理函数
 *
 * 注意：
 *   - addProject 会先把项目落地到 agent，再把可导入的 launch 配置写入内存草稿
 *   - 已存在 service 的项目不会被 VS Code launch 导入结果覆盖
 */
export function useAddProjectFlow() {
  const agentStore = useAgentStore()
  const { t } = useI18n()
  const editorProject = ref<Project | null>(null)
  const editorIsNew = ref(false)

  async function tryImportVscodeLaunch(created: Project): Promise<void> {
    let configs
    try {
      configs = await api.getVscodeLaunch(created.id)
    } catch {
      // 无 launch.json 或解析失败时静默跳过，不阻塞添加项目。
      return
    }
    if (!configs || configs.length === 0) return

    const confirmed = await ask(
      t('settings.projects.importVscodeMessage', { count: configs.length }),
      { title: t('settings.projects.importVscodeTitle'), kind: 'info' },
    )
    if (!confirmed) return

    // 已有 service（来自已有 config 文件）时不覆盖，避免丢弃用户维护的项目配置。
    if (!emptyProjectHasNoServices(created)) return

    if (!created.environments) created.environments = []
    let devEnv = created.environments.find(e => e.is_dev) ?? created.environments[0]
    if (!devEnv) {
      devEnv = { id: '', name: 'dev', is_dev: true, order: 0 }
      created.environments.push(devEnv)
    }
    const devEnvName = devEnv.name

    created.services = configs.map((config, index) => ({
      id: '',
      project_id: created.id,
      name: config.name,
      required: false,
      order: index,
      status: '' as const,
      deployments: [{
        id: '',
        env_name: devEnvName,
        location: 'local' as const,
        command: config.command,
        work_dir: config.work_dir,
        env: config.env,
        status: '',
      }],
    }))
  }

  function closeEditor() {
    editorProject.value = null
    editorIsNew.value = false
  }

  function onEditorSaved() {
    closeEditor()
  }

  async function addProject() {
    const selected = await open({ directory: true, multiple: false, title: t('settings.projects.selectProjectRootTitle') })
    if (!selected || Array.isArray(selected)) return
    try {
      const created = await agentStore.addProject(selected)
      await tryImportVscodeLaunch(created)
      editorProject.value = created
      editorIsNew.value = true
    } catch (error) {
      const msg = error instanceof Error ? error.message : String(error)
      await message(msg, { title: t('settings.projects.unableAddProject'), kind: 'error' })
    }
  }

  return {
    editorProject,
    editorIsNew,
    addProject,
    closeEditor,
    onEditorSaved,
  }
}
