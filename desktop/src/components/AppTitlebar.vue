<!--
AppTitlebar：Windows 主窗口自绘标题栏。

职责：
  - 在 Windows 无原生 decorations 时承载应用标识、菜单入口和窗口控制按钮
  - 将菜单弹出和最小化、最大化、关闭动作交给 Tauri window/menu API

边界：
  - 不接管 macOS 系统菜单栏
  - 不改变运行态工作区、设置页或 onboarding 的内容布局
-->
<script setup lang="ts">
import { LogicalPosition } from '@tauri-apps/api/dpi'
import { Menu, type MenuOptions } from '@tauri-apps/api/menu'
import { getCurrentWindow } from '@tauri-apps/api/window'

type TitlebarMenuId = 'file' | 'edit' | 'view' | 'window' | 'help'
type TitlebarMenuItems = NonNullable<MenuOptions['items']>

const appWindow = getCurrentWindow()
const titlebarMenuLabels: Array<{ id: TitlebarMenuId; label: string }> = [
  { id: 'file', label: '文件' },
  { id: 'edit', label: '编辑' },
  { id: 'view', label: '视图' },
  { id: 'window', label: '窗口' },
  { id: 'help', label: '帮助' },
]
const titlebarMenuItems: Record<TitlebarMenuId, TitlebarMenuItems> = {
  file: [{ text: '暂无文件项', enabled: false }],
  edit: [
    { item: 'Undo', text: '撤销' },
    { item: 'Redo', text: '重做' },
    { item: 'Separator' },
    { item: 'Cut', text: '剪切' },
    { item: 'Copy', text: '复制' },
    { item: 'Paste', text: '粘贴' },
    { item: 'SelectAll', text: '全选' },
  ],
  view: [{ text: '暂无视图项', enabled: false }],
  window: [
    { item: 'Minimize', text: '最小化' },
    { item: 'CloseWindow', text: '关闭窗口' },
  ],
  help: [{ text: '暂无帮助项', enabled: false }],
}

function startWindowDrag(event: MouseEvent) {
  if (event.buttons !== 1) return
  void appWindow.startDragging().catch(() => undefined)
}

async function openTitlebarMenu(menuId: TitlebarMenuId, event: MouseEvent) {
  const target = event.currentTarget
  if (!(target instanceof HTMLElement)) return

  const rect = target.getBoundingClientRect()
  const menu = await Menu.new({ items: titlebarMenuItems[menuId] })
  await menu.popup(new LogicalPosition(rect.left, rect.bottom), appWindow)
}

function minimizeWindow() {
  void appWindow.minimize().catch(() => undefined)
}

function toggleWindowMaximize() {
  void appWindow.toggleMaximize().catch(() => undefined)
}

function closeWindow() {
  void appWindow.close().catch(() => undefined)
}
</script>

<template>
  <header
    class="app-titlebar"
    data-test="app-titlebar"
    data-tauri-drag-region
    @mousedown="startWindowDrag"
  >
    <div class="app-titlebar-brand" data-tauri-drag-region>
      <img class="app-titlebar-icon" src="/favicon.svg" alt="" data-tauri-drag-region>
      <span class="app-titlebar-name" data-tauri-drag-region>SuperDev</span>
    </div>
    <nav class="app-titlebar-menu" data-test="app-titlebar-menu" aria-label="应用菜单">
      <button
        v-for="item in titlebarMenuLabels"
        :key="item.id"
        class="app-titlebar-menu-button"
        type="button"
        :data-test="`app-titlebar-menu-${item.id}`"
        aria-haspopup="menu"
        @mousedown.stop
        @click="openTitlebarMenu(item.id, $event)"
      >
        {{ item.label }}
      </button>
    </nav>
    <div class="app-titlebar-drag-fill" data-tauri-drag-region aria-hidden="true" />
    <div class="app-titlebar-controls" aria-label="窗口控制">
      <button
        class="app-titlebar-control"
        data-test="app-titlebar-minimize"
        type="button"
        aria-label="最小化"
        title="最小化"
        @mousedown.stop
        @click="minimizeWindow"
      >
        <span aria-hidden="true">−</span>
      </button>
      <button
        class="app-titlebar-control"
        data-test="app-titlebar-maximize"
        type="button"
        aria-label="最大化"
        title="最大化"
        @mousedown.stop
        @click="toggleWindowMaximize"
      >
        <span aria-hidden="true">□</span>
      </button>
      <button
        class="app-titlebar-control close"
        data-test="app-titlebar-close"
        type="button"
        aria-label="关闭"
        title="关闭"
        @mousedown.stop
        @click="closeWindow"
      >
        <span aria-hidden="true">×</span>
      </button>
    </div>
  </header>
</template>

<style scoped>
.app-titlebar {
  display: flex;
  height: 34px;
  flex: 0 0 34px;
  align-items: center;
  border-bottom: 1px solid rgba(0, 0, 0, 0.12);
  background: #ffffff;
  color: #111827;
  font-size: 12px;
  line-height: 1;
}

.app-titlebar-brand {
  display: inline-flex;
  height: 100%;
  flex: 0 0 auto;
  align-items: center;
  gap: 7px;
  padding: 0 8px 0 12px;
}

.app-titlebar-icon {
  width: 15px;
  height: 15px;
  flex: 0 0 auto;
}

.app-titlebar-name {
  max-width: 120px;
  overflow: hidden;
  color: #111827;
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.app-titlebar-menu {
  display: inline-flex;
  height: 100%;
  flex: 0 0 auto;
  align-items: center;
  gap: 1px;
}

.app-titlebar-menu-button,
.app-titlebar-control {
  border: 0;
  background: transparent;
  color: inherit;
  cursor: default;
  font: inherit;
}

.app-titlebar-menu-button {
  height: 26px;
  padding: 0 9px;
  border-radius: 3px;
}

.app-titlebar-menu-button:hover,
.app-titlebar-menu-button:focus-visible {
  background: rgba(0, 0, 0, 0.08);
  outline: none;
}

.app-titlebar-drag-fill {
  align-self: stretch;
  min-width: 40px;
  flex: 1 1 auto;
}

.app-titlebar-controls {
  display: inline-flex;
  height: 100%;
  flex: 0 0 auto;
  align-items: stretch;
}

.app-titlebar-control {
  display: inline-flex;
  width: 46px;
  height: 100%;
  align-items: center;
  justify-content: center;
  padding: 0;
  color: #111827;
  font-size: 15px;
}

.app-titlebar-control:hover,
.app-titlebar-control:focus-visible {
  background: rgba(0, 0, 0, 0.1);
  outline: none;
}

.app-titlebar-control.close:hover,
.app-titlebar-control.close:focus-visible {
  background: #e81123;
  color: #ffffff;
}
</style>
