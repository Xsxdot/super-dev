import { defineConfig } from 'vitepress'

const repo = 'https://github.com/Xsxdot/super-dev'

/**
 * Custom shiki themes mapped from Kimi `color.syntax.*` design tokens.
 * (Shiki 2.x no longer ships the `css-variables` theme, so the token
 * values are inlined here instead.)
 */
const kimiLightTheme = {
  name: 'kimi-light',
  type: 'light' as const,
  colors: {
    'editor.foreground': '#1a1a1a',
    'editor.background': '#f5f5f5', // color.background.secondary
  },
  settings: [
    { scope: ['comment', 'punctuation.definition.comment'], settings: { foreground: '#B2B2B2' } },
    { scope: ['keyword', 'storage', 'storage.type'], settings: { foreground: '#034C7C' } },
    { scope: ['string', 'string.quoted'], settings: { foreground: '#A44185' } },
    {
      scope: ['entity.name.function', 'support.function', 'meta.function-call'],
      settings: { foreground: '#7EB233' },
    },
    { scope: ['variable', 'variable.other', 'entity.name.variable'], settings: { foreground: '#2F86D2' } },
    { scope: ['constant.numeric', 'constant.language'], settings: { foreground: '#174781' } },
    { scope: ['keyword.operator', 'punctuation'], settings: { foreground: '#0991B6' } },
    { scope: ['markup.underline.link'], settings: { foreground: '#1783FF' } },
  ],
}

const kimiDarkTheme = {
  name: 'kimi-dark',
  type: 'dark' as const,
  colors: {
    'editor.foreground': '#d6d6d6',
    'editor.background': '#1f1f1f', // color.background.secondary (dark)
  },
  settings: [
    { scope: ['comment', 'punctuation.definition.comment'], settings: { foreground: '#B2B2B2' } },
    { scope: ['keyword', 'storage', 'storage.type'], settings: { foreground: '#C586C0' } },
    { scope: ['string', 'string.quoted'], settings: { foreground: '#CE9178' } },
    {
      scope: ['entity.name.function', 'support.function', 'meta.function-call'],
      settings: { foreground: '#DCDCAA' },
    },
    { scope: ['variable', 'variable.other', 'entity.name.variable'], settings: { foreground: '#9CDCFE' } },
    { scope: ['constant.numeric', 'constant.language'], settings: { foreground: '#B5CEA8' } },
    { scope: ['keyword.operator', 'punctuation'], settings: { foreground: '#D4D4D4' } },
    { scope: ['markup.underline.link'], settings: { foreground: '#1A88FF' } },
  ],
}

export default defineConfig({
  title: 'SuperDev',
  description:
    'SuperDev gives AI coding agents runtime senses and safe hands via MCP: a unified runtime console, logs, breakpoint debugging, browser control, deploy pipelines, and approval-gated operations.',
  cleanUrls: true,
  lastUpdated: false,

  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/logo.svg' }],
    ['meta', { name: 'theme-color', content: '#1783ff' }],
    ['meta', { property: 'og:title', content: 'SuperDev — Runtime senses for your AI' }],
    [
      'meta',
      {
        property: 'og:description',
        content:
          'Open-source desktop app + local agent that gives AI coding agents eyes and hands in the runtime, gated by human approval.',
      },
    ],
    ['meta', { property: 'og:image', content: '/screenshot-en.png' }],
  ],

  markdown: {
    // Code highlighting uses Kimi color.syntax.* tokens via custom themes above.
    theme: { light: kimiLightTheme, dark: kimiDarkTheme },
  },

  themeConfig: {
    logo: '/logo.svg',
    socialLinks: [{ icon: 'github', link: repo }],
    nav: [
      { text: 'Capabilities', link: '/#capabilities' },
      { text: 'Safety', link: '/#safety' },
      { text: 'Quick start', link: '/#quickstart' },
      { text: 'Roadmap', link: '/#roadmap' },
    ],
  },

  locales: {
    root: {
      label: 'English',
      lang: 'en-US',
      themeConfig: {
        nav: [
          { text: 'Capabilities', link: '/#capabilities' },
          { text: 'Safety', link: '/#safety' },
          { text: 'Quick start', link: '/#quickstart' },
          { text: 'Roadmap', link: '/#roadmap' },
        ],
      },
    },
    zh: {
      label: '简体中文',
      lang: 'zh-CN',
      link: '/zh/',
      description:
        'SuperDev 是开源桌面应用 + 本地 agent，通过 MCP 为 AI 编程助手补上运行时感官与安全双手：统一运行态、日志、断点调试、浏览器控制、部署流水线与审批门控。',
      themeConfig: {
        nav: [
          { text: '能力', link: '/zh/#capabilities' },
          { text: '安全', link: '/zh/#safety' },
          { text: '快速开始', link: '/zh/#quickstart' },
          { text: '路线图', link: '/zh/#roadmap' },
        ],
      },
    },
  },
})
