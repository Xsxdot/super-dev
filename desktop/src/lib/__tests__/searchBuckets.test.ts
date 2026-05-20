/**
 * searchBuckets 测试跨服务日志上下文的时间栅格对齐。
 *
 * 职责：
 *   - 验证 1 秒时间栅格按服务对齐
 *   - 验证所有服务都没有日志的时间段不会产生栅格
 *   - 验证单个服务缺日志时产生空白占位
 *
 * 边界：
 *   - 不测 DOM 高度，组件层根据 row.entries 计算实际高度
 *   - 不负责搜索 API 请求
 */
import { describe, expect, it } from 'vitest'
import { buildSearchBuckets } from '../searchBuckets'
import type { LogEntry } from '../../api/agent'

function log(id: number, serviceId: string, timestamp: string, message: string): LogEntry {
  return {
    id,
    service_id: serviceId,
    run_id: 'run-1',
    timestamp,
    level: 'INFO',
    message,
    stream: 'stdout',
  }
}

describe('buildSearchBuckets', () => {
  it('按 1 秒时间栅格生成跨服务行，并给缺日志服务留空白', () => {
    const buckets = buildSearchBuckets({
      serviceIds: ['svc-a', 'svc-b', 'svc-c'],
      itemsByService: {
        'svc-a': [log(1, 'svc-a', '2026-05-20T22:41:32.100Z', 'a')],
        'svc-b': [log(2, 'svc-b', '2026-05-20T22:41:32.300Z', 'b')],
        'svc-c': [],
      },
    })

    expect(buckets).toHaveLength(1)
    expect(buckets[0].bucketLabel).toBe('22:41:32')
    expect(buckets[0].cells['svc-a'].entries.map(e => e.message)).toEqual(['a'])
    expect(buckets[0].cells['svc-b'].entries.map(e => e.message)).toEqual(['b'])
    expect(buckets[0].cells['svc-c'].entries).toEqual([])
    expect(buckets[0].cells['svc-c'].blank).toBe(true)
  })

  it('不会为所有服务都没有日志的秒生成空栅格', () => {
    const buckets = buildSearchBuckets({
      serviceIds: ['svc-a', 'svc-b'],
      itemsByService: {
        'svc-a': [log(1, 'svc-a', '2026-05-20T22:41:33.100Z', 'a')],
        'svc-b': [],
      },
    })

    expect(buckets.map(b => b.bucketLabel)).toEqual(['22:41:33'])
  })

  it('同一服务同一秒内多条日志保留原顺序', () => {
    const buckets = buildSearchBuckets({
      serviceIds: ['svc-a'],
      itemsByService: {
        'svc-a': [
          log(1, 'svc-a', '2026-05-20T22:41:32.100Z', 'first'),
          log(2, 'svc-a', '2026-05-20T22:41:32.200Z', 'second'),
        ],
      },
    })

    expect(buckets[0].cells['svc-a'].entries.map(e => e.message)).toEqual(['first', 'second'])
  })
})
