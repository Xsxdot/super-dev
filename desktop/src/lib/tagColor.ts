// tagColor 提供 tag 到 CSS 颜色的稳定映射。
//
// 职责：
//   - 为常见环境 tag 提供语义色
//   - 为未知 tag 提供稳定 hash 色，保证刷新后颜色不漂移
//
// 边界：
//   - 不存储用户配置
//   - 不处理 UI 组件的展示逻辑
const PRESET: Record<string, string> = {
  prod: '#d9534f',
  test: '#f0ad4e',
  temp: '#ec843e',
  dev: '#5bc0de',
  staging: '#9966cc',
}

const PALETTE = [
  '#4e79a7',
  '#f28e2b',
  '#e15759',
  '#76b7b2',
  '#59a14f',
  '#edc949',
  '#af7aa1',
  '#ff9da7',
  '#9c755f',
  '#bab0ab',
]

export function tagColor(tag: string): string {
  if (PRESET[tag]) return PRESET[tag]
  let hash = 0
  for (let i = 0; i < tag.length; i++) {
    hash = (hash * 31 + tag.charCodeAt(i)) >>> 0
  }
  return PALETTE[hash % PALETTE.length]
}
