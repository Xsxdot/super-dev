/**
 * portMirrorView 测试。
 *
 * 职责：
 *   - 覆盖 mirrorRowsForDeployment 的过滤（按 deployment 归属、剔除 duplicate 声明失败）、
 *     排序（按端口升序）与字段派生（label/conflict/openUrl/hostId/hostName）
 *   - 覆盖 mirrorSummaryForDeployment 的 none/ok/conflict 三态，含冲突优先级与多端口场景
 *   - 覆盖 mirrorRowsForHost 按 host 归属过滤（Task 11 节点卡镜像区消费），与
 *     mirrorRowsForDeployment 共享同一套 duplicate 剔除/排序/字段派生规则
 *
 * 边界：
 *   - 纯函数单元测试，不 mount 组件、不涉及 Pinia、不涉及 i18n
 */
import { describe, expect, it } from 'vitest'
import { mirrorRowsForDeployment, mirrorRowsForHost, mirrorSummaryForDeployment } from '@/lib/portMirrorView'
import type { MirrorStatus } from '@/api/agent'

// mirror 构造一条测试用 MirrorStatus，默认是一条正常建立的 active 镜像。
function mirror(overrides: Partial<MirrorStatus> = {}): MirrorStatus {
  return {
    host_id: 'h1',
    host_name: 'dev-box',
    deployment_id: 'dep1',
    service_name: 'api',
    port: 9100,
    state: 'active',
    updated_at: '2026-06-06T10:00:00Z',
    ...overrides,
  }
}

describe('portMirrorView', () => {
  describe('mirrorRowsForDeployment', () => {
    it('只返回目标 deployment 自己的镜像行', () => {
      const rows = mirrorRowsForDeployment('dep1', [
        mirror({ deployment_id: 'dep1', port: 9100 }),
        mirror({ deployment_id: 'dep2', port: 9200 }),
      ])

      expect(rows).toHaveLength(1)
      expect(rows[0].port).toBe(9100)
    })

    it('多端口按端口号升序排序', () => {
      const rows = mirrorRowsForDeployment('dep1', [
        mirror({ port: 9300 }),
        mirror({ port: 9100 }),
        mirror({ port: 9200 }),
      ])

      expect(rows.map(r => r.port)).toEqual([9100, 9200, 9300])
    })

    it('duplicate_port_declaration 的 failed 条目不进 rows（从未真正尝试转发，不是一次转发失败）', () => {
      const rows = mirrorRowsForDeployment('dep1', [
        mirror({ port: 9100, state: 'failed', error: 'duplicate_port_declaration' }),
      ])

      expect(rows).toHaveLength(0)
    })

    it('非 duplicate 的 failed 条目仍然进 rows（真实尝试过转发，用户可能需要 retry）', () => {
      const rows = mirrorRowsForDeployment('dep1', [
        mirror({ port: 9100, state: 'failed', error: 'ssh_forward_error' }),
      ])

      expect(rows).toHaveLength(1)
      expect(rows[0].state).toBe('failed')
    })

    it('只有 state 为 active 时才有 openUrl，指向 127.0.0.1 同端口', () => {
      const rows = mirrorRowsForDeployment('dep1', [
        mirror({ port: 9100, state: 'active' }),
        mirror({ port: 9200, state: 'conflict' }),
        mirror({ port: 9300, state: 'pending' }),
        mirror({ port: 9400, state: 'failed', error: 'ssh_forward_error' }),
      ])

      const byPort = (p: number) => rows.find(r => r.port === p)!
      expect(byPort(9100).openUrl).toBe('http://127.0.0.1:9100')
      expect(byPort(9200).openUrl).toBeUndefined()
      expect(byPort(9300).openUrl).toBeUndefined()
      expect(byPort(9400).openUrl).toBeUndefined()
    })

    it('conflict 字段只在 state 为 conflict 时为 true', () => {
      const rows = mirrorRowsForDeployment('dep1', [
        mirror({ port: 9100, state: 'active' }),
        mirror({ port: 9200, state: 'conflict' }),
      ])

      expect(rows.find(r => r.port === 9100)!.conflict).toBe(false)
      expect(rows.find(r => r.port === 9200)!.conflict).toBe(true)
    })

    it('透传 hostId/hostName，供底栏 chip 文本与冲突 emit 载荷使用', () => {
      const rows = mirrorRowsForDeployment('dep1', [
        mirror({ port: 9100, host_id: 'h9', host_name: 'dev-box-9' }),
      ])

      expect(rows[0].hostId).toBe('h9')
      expect(rows[0].hostName).toBe('dev-box-9')
    })

    it('label 是不含自然语言词汇的结构性文本；active 态含解析后的本机地址', () => {
      const rows = mirrorRowsForDeployment('dep1', [
        mirror({ port: 9100, state: 'active' }),
        mirror({ port: 9200, state: 'conflict' }),
      ])

      expect(rows.find(r => r.port === 9100)!.label).toBe(':9100 ⇄ 127.0.0.1:9100')
      expect(rows.find(r => r.port === 9200)!.label).toBe(':9200')
    })
  })

  describe('mirrorRowsForHost', () => {
    it('只返回目标 host 自己的镜像行，不区分来自哪个 deployment（节点卡按 host 汇总全部端口）', () => {
      const rows = mirrorRowsForHost('h1', [
        mirror({ host_id: 'h1', deployment_id: 'dep1', port: 9100 }),
        mirror({ host_id: 'h1', deployment_id: 'dep2', port: 9200 }),
        mirror({ host_id: 'h2', deployment_id: 'dep3', port: 9300 }),
      ])

      expect(rows.map(r => r.port)).toEqual([9100, 9200])
    })

    it('多端口按端口号升序排序', () => {
      const rows = mirrorRowsForHost('h1', [
        mirror({ host_id: 'h1', port: 9300 }),
        mirror({ host_id: 'h1', port: 9100 }),
        mirror({ host_id: 'h1', port: 9200 }),
      ])

      expect(rows.map(r => r.port)).toEqual([9100, 9200, 9300])
    })

    it('duplicate_port_declaration 的 failed 条目不进 rows（与 mirrorRowsForDeployment 共享同一条剔除规则）', () => {
      const rows = mirrorRowsForHost('h1', [
        mirror({ host_id: 'h1', port: 9100, state: 'failed', error: 'duplicate_port_declaration' }),
      ])

      expect(rows).toHaveLength(0)
    })

    it('透传 conflict/hostId/hostName 字段，供节点卡冲突行点击 emit 载荷使用', () => {
      const rows = mirrorRowsForHost('h1', [
        mirror({ host_id: 'h1', host_name: 'dev-box', port: 5173, state: 'conflict' }),
      ])

      expect(rows[0].conflict).toBe(true)
      expect(rows[0].hostId).toBe('h1')
      expect(rows[0].hostName).toBe('dev-box')
    })

    it('目标 host 没有任何镜像时返回空数组', () => {
      expect(mirrorRowsForHost('h1', [mirror({ host_id: 'h2' })])).toEqual([])
    })
  })

  describe('mirrorSummaryForDeployment', () => {
    it('没有任何镜像时返回 none', () => {
      expect(mirrorSummaryForDeployment('dep1', [])).toEqual({ kind: 'none' })
    })

    it('该 deployment 在别的 deployment 有镜像时仍返回 none', () => {
      const summary = mirrorSummaryForDeployment('dep1', [mirror({ deployment_id: 'dep2', port: 9100 })])

      expect(summary).toEqual({ kind: 'none' })
    })

    it('只剩 duplicate 失败条目时视为 none', () => {
      const summary = mirrorSummaryForDeployment('dep1', [
        mirror({ port: 9100, state: 'failed', error: 'duplicate_port_declaration' }),
      ])

      expect(summary).toEqual({ kind: 'none' })
    })

    it('只有 pending 条目时视为 none（还没有可展示的确定态）', () => {
      const summary = mirrorSummaryForDeployment('dep1', [mirror({ port: 9100, state: 'pending' })])

      expect(summary).toEqual({ kind: 'none' })
    })

    it('全部 active 时返回 ok 与升序端口列表', () => {
      const summary = mirrorSummaryForDeployment('dep1', [
        mirror({ port: 9200, state: 'active' }),
        mirror({ port: 9100, state: 'active' }),
      ])

      expect(summary).toEqual({ kind: 'ok', ports: [9100, 9200] })
    })

    it('存在 conflict 时返回 conflict 与该端口，即使同时有 active 端口', () => {
      const summary = mirrorSummaryForDeployment('dep1', [
        mirror({ port: 9100, state: 'active' }),
        mirror({ port: 9200, state: 'conflict' }),
      ])

      expect(summary).toEqual({ kind: 'conflict', port: 9200 })
    })

    it('多个 conflict 时取端口最小的一个代表（服务行 meta 只有一行空间）', () => {
      const summary = mirrorSummaryForDeployment('dep1', [
        mirror({ port: 9300, state: 'conflict' }),
        mirror({ port: 9100, state: 'conflict' }),
      ])

      expect(summary).toEqual({ kind: 'conflict', port: 9100 })
    })
  })
})
