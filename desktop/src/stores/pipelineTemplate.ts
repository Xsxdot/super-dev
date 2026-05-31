/**
 * 流水线模板 Store。
 *
 * 职责：
 *   - 加载 builtin/user/project 模板列表
 *   - 导入本地模板后刷新列表
 *
 * 边界：
 *   - 不解析模板 YAML
 *   - 不保存 deployment 配置
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, type PipelineTemplateSummary } from '@/api/agent'

export const usePipelineTemplateStore = defineStore('pipelineTemplate', () => {
  const templates = ref<PipelineTemplateSummary[]>([])
  const loading = ref(false)

  async function loadTemplates() {
    loading.value = true
    try {
      templates.value = (await api.listPipelineTemplates()).items
    } finally {
      loading.value = false
    }
  }

  async function importTemplate(path: string) {
    await api.importPipelineTemplate(path)
    await loadTemplates()
  }

  return { templates, loading, loadTemplates, importTemplate }
})
