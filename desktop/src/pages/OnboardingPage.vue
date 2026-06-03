<!--
零操作 Onboarding 引导页

职责：
  - 选择编程智能体
  - 触发 MCP 一键安装并展示成功/失败/手动兜底
  - 展示并复制 AI 启动提示词

边界：
  - 不直接写智能体配置文件，安装由 Tauri command 完成
  - 不注册示例项目，示例落地由 agent 启动钩子完成
-->
<script setup lang="ts">
import { useRouter } from 'vue-router'
import { codingAgents, useOnboardingStore } from '@/stores/onboarding'
import { useSettingsStore } from '@/stores/settings'

const router = useRouter()
const onboarding = useOnboardingStore()
const settings = useSettingsStore()

async function copyPrompt() {
  await navigator.clipboard.writeText(onboarding.demoPrompt)
}

async function finish() {
  await settings.setOnboardingCompleted(true)
  await router.push('/')
}
</script>

<template>
  <main class="onboarding-page">
    <section class="onboarding-shell">
      <header class="onboarding-header">
        <h1>SuperDev</h1>
        <p>让 AI 安全地操作你的本地环境。</p>
      </header>

      <section class="step-block">
        <div class="step-index">1</div>
        <div class="step-body">
          <h2>选择你的编程智能体</h2>
          <div class="agent-grid">
            <button
              v-for="agent in codingAgents"
              :key="agent.id"
              type="button"
              class="agent-card"
              :class="{ selected: onboarding.selectedAgent === agent.id }"
              :data-test="`agent-${agent.id}`"
              @click="onboarding.selectedAgent = agent.id"
            >
              {{ agent.label }}
            </button>
          </div>
        </div>
      </section>

      <section class="step-block">
        <div class="step-index">2</div>
        <div class="step-body">
          <h2>安装 MCP 连接</h2>
          <button
            class="primary-btn"
            data-test="install-mcp"
            type="button"
            :disabled="onboarding.installing"
            @click="onboarding.installSelectedMcp"
          >
            {{ onboarding.installing ? '安装中...' : '安装' }}
          </button>
          <p v-if="onboarding.installOutcome" class="state-ok" data-test="install-success">
            {{ onboarding.installOutcome.already_present ? '已是最新' : '已装好' }} ·
            {{ onboarding.installOutcome.config_path }}
          </p>
          <p v-if="onboarding.installOutcome?.backup_path" class="state-muted">
            已备份：{{ onboarding.installOutcome.backup_path }}
          </p>
          <div v-if="onboarding.installError" class="fallback" data-test="install-error">
            <p>{{ onboarding.installError }}</p>
            <p v-if="onboarding.installHint" class="state-muted">
              配置文件：{{ onboarding.installHint.config_path }}
            </p>
            <pre v-if="onboarding.installHint?.manual_config">{{ onboarding.installHint.manual_config }}</pre>
          </div>
        </div>
      </section>

      <section class="step-block">
        <div class="step-index">3</div>
        <div class="step-body">
          <h2>把这句话发给 AI</h2>
          <div class="prompt-box" data-test="demo-prompt">{{ onboarding.demoPrompt }}</div>
          <div class="actions">
            <button class="secondary-btn" data-test="copy-prompt" type="button" @click="copyPrompt">
              复制
            </button>
            <button class="primary-btn" data-test="finish-onboarding" type="button" @click="finish">
              我已发给 AI
            </button>
          </div>
        </div>
      </section>
    </section>
  </main>
</template>

<style scoped>
.onboarding-page {
  min-height: 100vh;
  background: var(--bg-primary);
  color: var(--text-primary);
  overflow: auto;
}

.onboarding-shell {
  max-width: 860px;
  margin: 0 auto;
  padding: 34px 20px 42px;
}

.onboarding-header h1 {
  margin: 0;
  font-size: 28px;
  line-height: 1.2;
}

.onboarding-header p,
.state-muted {
  color: var(--text-secondary);
}

.step-block {
  display: grid;
  grid-template-columns: 34px 1fr;
  gap: 14px;
  padding: 20px 0;
  border-top: 1px solid var(--border-secondary);
}

.step-index {
  width: 28px;
  height: 28px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  background: var(--accent);
  font-weight: 700;
}

h2 {
  margin: 0 0 12px;
  font-size: 16px;
  line-height: 1.3;
}

.agent-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
}

.agent-card,
.primary-btn,
.secondary-btn {
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 10px 12px;
  color: var(--text-primary);
  cursor: pointer;
}

.agent-card {
  background: var(--bg-elevated);
  text-align: left;
}

.agent-card.selected {
  border-color: var(--accent);
  background: var(--bg-overlay);
}

.primary-btn {
  background: var(--accent);
  border-color: var(--accent);
}

.secondary-btn {
  background: transparent;
}

.state-ok {
  color: var(--status-running);
}

.fallback {
  margin-top: 10px;
  border: 1px solid var(--status-failed);
  border-radius: 8px;
  padding: 10px;
}

pre,
.prompt-box {
  user-select: text;
  white-space: pre-wrap;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-elevated);
  padding: 12px;
}

.actions {
  display: flex;
  gap: 10px;
  margin-top: 12px;
}

@media (max-width: 640px) {
  .agent-grid {
    grid-template-columns: 1fr;
  }

  .step-block {
    grid-template-columns: 1fr;
  }
}
</style>
