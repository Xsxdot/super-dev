/**
 * mirrorConflictModalStore 测试端口镜像冲突详情弹窗的开关状态。
 *
 * 职责：
 *   - 验证 open 记录 {hostId, port} 目标
 *   - 验证 close 清空目标
 *   - 验证连续 open 会覆盖为最新目标（不排队、不叠加）
 *
 * 边界：
 *   - 不涉及镜像数据本身（那是 portMirrorStore 的职责）
 *   - 不渲染任何 Vue 组件
 */
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { useMirrorConflictModalStore } from '@/stores/mirrorConflictModal'

describe('mirrorConflictModalStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('初始状态没有打开任何目标', () => {
    const store = useMirrorConflictModalStore()

    expect(store.target).toBeNull()
  })

  it('open 记录 hostId/port 目标', () => {
    const store = useMirrorConflictModalStore()

    store.open('h1', 5173)

    expect(store.target).toEqual({ hostId: 'h1', port: 5173 })
  })

  it('close 清空目标', () => {
    const store = useMirrorConflictModalStore()
    store.open('h1', 5173)

    store.close()

    expect(store.target).toBeNull()
  })

  it('连续 open 用最新目标覆盖上一个，不叠加排队（弹窗一次只展示一个冲突）', () => {
    const store = useMirrorConflictModalStore()

    store.open('h1', 5173)
    store.open('h2', 9100)

    expect(store.target).toEqual({ hostId: 'h2', port: 9100 })
  })
})
