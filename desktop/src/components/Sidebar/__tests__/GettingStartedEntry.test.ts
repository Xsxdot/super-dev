/**
 * GettingStartedEntry 测试起步旅程侧边栏入口。
 *
 * 职责：
 *   - 验证入口展示和进度文案
 *   - 验证浮层脱离侧栏裁剪上下文并保持在视口内
 *
 * 边界：
 *   - 不测试 GettingStartedPanel 内部交互
 *   - 不依赖真实浏览器布局引擎
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import GettingStartedEntry from '@/components/Sidebar/GettingStartedEntry.vue'
import { installTestI18n } from '@/test-utils/i18n'

vi.mock('@tauri-apps/plugin-dialog', () => ({
  open: vi.fn(),
}))

describe('GettingStartedEntry', () => {
  beforeEach(() => {
    localStorage.clear()
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('窄窗口中打开浮层时脱离侧栏裁剪并保持在视口内', async () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(500)
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(720)
    vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function getRect(this: Element) {
      const element = this
      if (element.classList.contains('gs-entry-wrap')) {
        return {
          x: 8,
          y: 560,
          width: 272,
          height: 58,
          left: 8,
          right: 280,
          top: 560,
          bottom: 618,
          toJSON: () => ({}),
        } as DOMRect
      }
      return {
        x: 0,
        y: 0,
        width: 0,
        height: 0,
        left: 0,
        right: 0,
        top: 0,
        bottom: 0,
        toJSON: () => ({}),
      } as DOMRect
    })

    const wrapper = mount(GettingStartedEntry, {
      attachTo: document.body,
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    await wrapper.find('[data-test="getting-started-entry"]').trigger('click')
    await wrapper.vm.$nextTick()

    const popover = document.body.querySelector<HTMLElement>('[data-test="getting-started-popover"]')
    expect(popover).not.toBeNull()
    expect(popover && wrapper.element.contains(popover)).toBe(false)
    expect(popover?.style.left).toBe('16px')
    expect(popover?.style.bottom).toBe('168px')
  })
})
