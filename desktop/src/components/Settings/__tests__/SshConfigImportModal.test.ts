/**
 * SshConfigImportModal 测试 SSH config 导入弹窗。
 *
 * 职责：
 *   - 验证弹窗打开时加载 ~/.ssh/config Host 条目
 *   - 验证多选后向父组件提交选中条目
 *
 * 边界：
 *   - 不读取真实 ~/.ssh/config
 */
import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import SshConfigImportModal from '@/components/Settings/SshConfigImportModal.vue'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      listSshConfigHosts: vi.fn().mockResolvedValue([
        {
          host: 'prod-01',
          hostname: '10.0.0.1',
          port: 22,
          user: 'deploy',
          identity_file: '~/.ssh/id_ed25519',
        },
        {
          host: 'prod-02',
          hostname: '10.0.0.2',
          port: 22,
          user: 'deploy',
        },
      ]),
    },
  }
})

describe('SshConfigImportModal', () => {
  it('显示 SSH config 条目列表', async () => {
    const wrapper = mount(SshConfigImportModal, { props: { visible: true } })
    await new Promise(resolve => setTimeout(resolve))
    await nextTick()

    const rows = wrapper.findAll('[data-test="ssh-import-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('prod-01')
  })

  it('多选后 emit import 携带选中条目', async () => {
    const wrapper = mount(SshConfigImportModal, { props: { visible: true } })
    await new Promise(resolve => setTimeout(resolve))
    await nextTick()

    await wrapper.findAll('[data-test="ssh-import-row"]')[0].trigger('click')
    await wrapper.findAll('[data-test="ssh-import-row"]')[1].trigger('click')
    await wrapper.find('[data-test="ssh-import-confirm"]').trigger('click')

    const emitted = wrapper.emitted('import')!
    expect(emitted).toHaveLength(1)
    expect((emitted[0][0] as unknown[])).toHaveLength(2)
  })
})
