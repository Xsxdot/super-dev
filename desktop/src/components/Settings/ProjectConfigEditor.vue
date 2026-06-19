<!--
ProjectConfigEditor：项目运行配置编辑器 modal 外壳。

职责：
  - 提供 settings modal 的遮罩、标题和宽屏容器
  - 复用 ProjectConfigSurface 完成配置草稿、校验和保存
  - 将保存/取消事件透传给父层

边界：
  - 不持有配置草稿
  - 不实现配置表单细节
-->
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { Project } from '@/api/agent'
import ProjectConfigSurface from './ProjectConfigSurface.vue'

defineProps<{ project: Project; isNew?: boolean }>()
const emit = defineEmits<{ saved: [Project]; cancel: [] }>()
const { t } = useI18n()
</script>

<template>
  <div class="settings-modal-backdrop" @click.self="emit('cancel')">
    <div class="settings-modal settings-modal-wide editor-body">
      <div class="settings-modal-header">
        <h2 class="settings-modal-title">{{ t('settings.projects.editConfig') }} · {{ project.name }}</h2>
      </div>

      <div class="settings-modal-body editor-content">
        <ProjectConfigSurface
          :project="project"
          :is-new="isNew"
          @saved="emit('saved', $event)"
          @cancel="emit('cancel')"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.editor-body {
  display: flex;
  flex-direction: column;
}
.editor-content {
  display: flex;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  overflow: hidden;
}
</style>
