/**
 * tagColor 测试 tag 到颜色的稳定映射。
 *
 * 职责：
 *   - 验证语义 tag 命中预设色
 *   - 验证未知 tag 通过 hash 稳定映射
 *
 * 边界：
 *   - 不关心具体 UI 渲染
 */
import { describe, it, expect } from 'vitest'
import { tagColor } from '@/lib/tagColor'

describe('tagColor', () => {
  it('预设 tag 走预设颜色', () => {
    expect(tagColor('prod')).toBe('#d9534f')
    expect(tagColor('test')).toBe('#f0ad4e')
  })

  it('同一 tag 总是返回同一颜色', () => {
    expect(tagColor('foo')).toBe(tagColor('foo'))
  })

  it('未知 tag 命中调色板', () => {
    expect(tagColor('unknown-tag')).toMatch(/^#[0-9a-f]{6}$/i)
  })
})
