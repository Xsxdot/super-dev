/**
 * 流水线模板 Store。
 *
 * 职责：
 *   - 加载 builtin/user/project 模板列表
 *   - 导入本地模板后刷新列表
 *   - 按 source/id/version 加载模板详情 YAML
 *
 * 边界：
 *   - 不解析模板 YAML
 *   - 不保存 deployment 配置
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, type PipelineTemplateDetail, type PipelineTemplateSummary } from '@/api/agent'

export const usePipelineTemplateStore = defineStore('pipelineTemplate', () => {
  const templates = ref<PipelineTemplateSummary[]>([])
  const details = ref<Record<string, PipelineTemplateDetail>>({})
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

  async function loadTemplateDetail(source: PipelineTemplateSummary['source'], id: string, version: string) {
    const key = `${source}://${id}@${version}`
    if (details.value[key]) return details.value[key]
    const detail = await api.getPipelineTemplate(source, id, version)
    details.value[key] = detail
    return detail
  }

  return { templates, details, loading, loadTemplates, importTemplate, loadTemplateDetail }
})
