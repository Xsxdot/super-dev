<script setup lang="ts">
import { computed } from 'vue'
import { LogicalPosition } from '@tauri-apps/api/dpi'
import { Menu, type MenuOptions } from '@tauri-apps/api/menu'
import { getCurrentWindow } from '@tauri-apps/api/window'
import { useAgentStore } from '@/stores/agent'
import { useNodeStore } from '@/stores/node'
import { useWorkspaceStore } from '@/stores/workspace'
import SidebarView from '@/components/Sidebar/SidebarView.vue'
import WorkspaceShell from '@/components/Workspace/WorkspaceShell.vue'
import WorkspaceTabs from '@/components/Workspace/WorkspaceTabs.vue'
import BottomBar from '@/components/BottomBar.vue'

const agentStore = useAgentStore()
const nodeStore = useNodeStore()
const workspace = useWorkspaceStore()
agentStore.startPolling()
void nodeStore.start()

const showAppTopbar = computed(() => !workspace.isRuntimeWorkspaceMaximized)
const showWorkspaceTabs = computed(() =>
  workspace.tabs.length > 0 && !workspace.isRuntimeWorkspaceMaximized,
)

const appWindow = getCurrentWindow()
type ChromeMenuId = 'edit' | 'window' | 'help'
type ChromeMenuItems = NonNullable<MenuOptions['items']>

const showChromeMenu = !isMacShell()
const chromeMenuLabels: Array<{ id: ChromeMenuId; label: string }> = [
  { id: 'edit', label: '编辑' },
  { id: 'window', label: '窗口' },
  { id: 'help', label: '帮助' },
]
const chromeMenuItems: Record<ChromeMenuId, ChromeMenuItems> = {
  edit: [
    { item: 'Undo', text: '撤销' },
    { item: 'Redo', text: '重做' },
    { item: 'Separator' },
    { item: 'Cut', text: '剪切' },
    { item: 'Copy', text: '复制' },
    { item: 'Paste', text: '粘贴' },
    { item: 'SelectAll', text: '全选' },
  ],
  window: [
    { item: 'Minimize', text: '最小化' },
    { item: 'CloseWindow', text: '关闭窗口' },
  ],
  help: [{ text: '暂无帮助项', enabled: false }],
}

function isMacShell() {
  return /mac/i.test(window.navigator.platform)
}

function startWindowDrag(event: MouseEvent) {
  if (event.buttons !== 1) return
  void appWindow.startDragging().catch(() => undefined)
}

async function openChromeMenu(menuId: ChromeMenuId, event: MouseEvent) {
  const target = event.currentTarget
  if (!(target instanceof HTMLElement)) return

  const rect = target.getBoundingClientRect()
  const menu = await Menu.new({ items: chromeMenuItems[menuId] })
  await menu.popup(new LogicalPosition(rect.left, rect.bottom), appWindow)
}
</script>

<template>
  <div class="main-layout" data-test="main-layout">
    <div
      v-if="showAppTopbar"
      class="app-topbar"
      data-test="app-topbar"
      data-tauri-drag-region
    >
      <div
        class="app-brand"
        data-test="app-brand"
        data-tauri-drag-region="deep"
        @mousedown="startWindowDrag"
      >
        <span class="app-brand-title" data-tauri-drag-region>SuperDev</span>
      </div>
      <nav v-if="showChromeMenu" class="app-chrome-menu" data-test="app-chrome-menu" aria-label="应用菜单">
        <button
          v-for="item in chromeMenuLabels"
          :key="item.id"
          class="app-chrome-menu-button"
          type="button"
          :data-test="`app-chrome-menu-${item.id}`"
          aria-haspopup="menu"
          @mousedown.stop
          @click="openChromeMenu(item.id, $event)"
        >
          {{ item.label }}
        </button>
      </nav>
      <div class="app-tabs-region" data-test="app-tabs-region">
        <WorkspaceTabs v-if="showWorkspaceTabs" />
        <div class="app-drag-fill" data-tauri-drag-region @mousedown="startWindowDrag" />
      </div>
    </div>
    <div class="main-content-row" data-test="main-content-row">
      <SidebarView v-if="!workspace.isRuntimeWorkspaceMaximized" />
      <div class="workspace-column">
        <WorkspaceShell />
      </div>
    </div>
    <BottomBar v-if="!workspace.isRuntimeWorkspaceMaximized" />
  </div>
</template>

<style scoped>
.main-layout {
  --sidebar-width: 280px;
  --app-topbar-height: 52px;
  display: flex;
  position: relative;
  height: 100vh;
  flex-direction: column;
  overflow: hidden;
  background: var(--bg-primary);
}

.app-topbar {
  z-index: 30;
  display: flex;
  height: var(--app-topbar-height);
  flex-shrink: 0;
  border-bottom: 1px solid var(--border-secondary);
  background: var(--bg-primary);
}

.app-brand {
  display: flex;
  width: var(--sidebar-width);
  height: 100%;
  flex-shrink: 0;
  align-items: center;
  padding: 0 18px 0 108px;
  border-right: 1px solid var(--border-secondary);
}

.app-chrome-menu {
  display: flex;
  height: 100%;
  flex-shrink: 0;
  align-items: center;
  gap: 2px;
  padding: 0 8px;
  border-right: 1px solid var(--border-secondary);
}

.app-chrome-menu-button {
  display: inline-flex;
  height: 28px;
  min-width: 42px;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--text-secondary);
  cursor: default;
  font: inherit;
  line-height: 1;
  padding: 0 9px;
}

.app-chrome-menu-button:hover,
.app-chrome-menu-button:focus-visible {
  background: var(--control-hover);
  color: var(--text-primary);
  outline: none;
}

.app-brand-title {
  overflow: hidden;
  color: var(--text-primary);
  font-size: 14px;
  font-weight: 700;
  line-height: 1;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.app-tabs-region {
  display: flex;
  min-width: 0;
  flex: 1;
  align-items: stretch;
  overflow: hidden;
}

.app-tabs-region :deep(.workspace-tabs) {
  height: 100%;
  min-width: 0;
  max-width: 100%;
  flex: 0 1 auto;
  align-items: flex-end;
  padding: 8px 8px 0;
  border-bottom: 0;
  background: transparent;
}

.app-tabs-region :deep(.workspace-tab) {
  height: 44px;
  padding: 0 14px;
  font-size: 13px;
}

.app-drag-fill {
  min-width: 80px;
  flex: 1;
}

.main-content-row {
  display: flex;
  min-height: 0;
  flex: 1;
  overflow: hidden;
}

.workspace-column {
  display: flex;
  min-width: 0;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  overflow: hidden;
}
</style>
