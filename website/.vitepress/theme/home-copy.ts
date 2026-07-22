export interface NavItem {
  label: string
  href: string
  /** hidden first when the viewport narrows */
  optional?: boolean
}

export interface RoadmapItem {
  title: string
  desc: string
  status: 'design' | 'research' | 'released'
  statusLabel: string
}

export interface HomeCopy {
  localeLabel: string
  localeHref: string
  nav: NavItem[]
  github: string
  hero: {
    headline: string
    subline: string
    paragraph: string
    primaryCta: string
    secondaryCta: string
    badges: string[]
    screenshotAlt: string
  }
  demo: {
    title: string
    caption: string
    videoAlt: string
  }
  pains: {
    title: string
    items: { quote: string; body: string }[]
  }
  capabilities: {
    title: string
    items: { icon: 'see' | 'inspect' | 'operate'; name: string; tagline: string; body: string }[]
  }
  safety: {
    title: string
    flow: { name: string; desc: string }[]
    bullets: string[]
  }
  projects: {
    title: string
    items: { title: string; body: string }[]
  }
  connectors: {
    title: string
    lead: string
    chips: string[]
    tiers: { label: string; desc: string }[]
    note: string
  }
  quickstart: {
    title: string
    steps: string[]
    promptHint: string
    prompt: string
  }
  roadmap: {
    title: string
    stats: { value: string; label: string }[]
    oss: string
    items: RoadmapItem[]
  }
  footer: {
    tagline: string
    links: { label: string; href: string }[]
  }
}

const REPO = 'https://github.com/Xsxdot/super-dev'

export const homeCopy: Record<'en' | 'zh', HomeCopy> = {
  en: {
    localeLabel: '简体中文',
    localeHref: '/zh/',
    nav: [
      // 8 EN labels can't fit the 1080px header container; Demo is reachable
      // from the hero CTA and Pains follows the hero, so nav keeps 6 anchors.
      { label: 'Capabilities', href: '#capabilities' },
      { label: 'Safety', href: '#safety' },
      { label: 'Projects', href: '#projects', optional: true },
      { label: 'Connectors', href: '#connectors', optional: true },
      { label: 'Quick start', href: '#quickstart' },
      { label: 'Roadmap', href: '#roadmap' },
    ],
    github: 'GitHub',
    hero: {
      headline: '“Can you run it and check?”',
      subline: '— how many times has your AI asked you today?',
      paragraph:
        'It writes code, but can’t run, see, or verify anything — you screenshot whether the service is up, click through pages for it, copy variable values back. SuperDev gives your AI its runtime eyes and hands back: it runs, sees, inspects, verifies, and deploys itself — and asks you only before touching your environment.',
      primaryCta: 'Download SuperDev v0.2.1',
      secondaryCta: 'Watch demo',
      badges: ['Apache-2.0', 'macOS / Linux / Windows', 'local-first', 'MCP'],
      screenshotAlt: 'SuperDev desktop app — unified runtime console',
    },
    demo: {
      title: 'Demo',
      caption: 'Demo video — coming soon.',
      videoAlt: 'SuperDev demo placeholder',
    },
    pains: {
      title: 'You’ve lived these moments',
      items: [
        {
          quote: '“Paste the logs for me.”',
          body: 'You’ve become your AI’s runtime API. Official guides literally teach you to paste kubectl output and screenshot Grafana for it.',
        },
        {
          quote: '“Screenshot the page for me.”',
          body: 'It changed the UI, but you do the acceptance testing. An AI that can’t see the rendered page is frontend-driving blindfolded.',
        },
        {
          quote: '“Set a breakpoint on line 47 and send me the variables.”',
          body: 'Brilliant reasoning, zero access to the runtime scene. Tutorials literally teach humans to be the AI’s debugger.',
        },
        {
          quote: 'Deploy? Better do it yourself.',
          body: 'Not because the AI can’t, but because you don’t dare let it. It touches your environment without asking; the fallout is all yours.',
        },
      ],
    },
    capabilities: {
      title: 'See · Inspect · Operate',
      items: [
        {
          icon: 'see',
          name: 'See',
          tagline: 'Unified runtime + logs',
          body: 'Services, processes, ports, Docker, and logs from every local and remote project in one place; your AI tails, searches, and pulls context directly — no more pasting. 75 MCP tools at its disposal.',
        },
        {
          icon: 'inspect',
          name: 'Inspect',
          tagline: 'Breakpoint debugging',
          body: 'Attach to live processes (no restart, same pid), stop at a source line, and read back the call stack, scopes, and variables in one call. Out of the box: Go / Python / Rust / C++; experimental: Node, JVM (Java/Kotlin).',
        },
        {
          icon: 'operate',
          name: 'Operate',
          tagline: 'Browser + deploy',
          body: 'Navigate, click, type, screenshot, and read console and network with your own Chromium (zero download) — it accepts its own frontend work; DAG pipelines build, deploy, and roll back.',
        },
      ],
    },
    safety: {
      title: 'The safety gate lives in the agent layer, not in prompt goodwill',
      flow: [
        { name: 'preview', desc: 'pre-check' },
        { name: 'approve', desc: 'desktop approval' },
        { name: 'one-time token', desc: 'fingerprint-bound · time-limited · single-use' },
        { name: 'audit', desc: 'full audit trail' },
      ],
      bullets: [
        'Write operations either run directly or wait for your “Approve” click on the desktop: the MCP call suspends automatically and resumes with a one-time token. debug_evaluate is audited per expression, browser evaluate goes through a trust switch, and control actions are logged redacted.',
        'Debug credentials: when your AI hits a login wall, it signs in legally with the test credentials you pre-configured — it never has to forge a token.',
      ],
    },
    projects: {
      title: 'One desktop for every project',
      items: [
        {
          title: 'Unified local + remote model',
          body: 'Import remote hosts over SSH; processes / launchd / systemd / Docker / nginx static sites share one source of truth — the desktop and your AI see the same world.',
        },
        {
          title: '12 built-in pipeline templates',
          body: 'Go / Node / Python / Java / Rust / PHP / Vue+Go… build, artifacts, deploy, and rollback — all replayable.',
        },
        {
          title: 'Declarative ingress',
          body: 'Domains, DNS (Cloudflare / Aliyun / manual), nginx reverse proxy, ACME DNS-01 certificates — state convergence, not script piles.',
        },
      ],
    },
    connectors: {
      title: 'Works with the AI you already use',
      lead: 'Don’t switch your AI — complete its second half. Seven built-in connectors; one click installs MCP + Skill:',
      chips: ['Claude Code', 'Codex', 'Cursor', 'OpenCode', 'OpenClaw', 'Hermes', 'Kimi Code'],
      tiers: [
        { label: 'Full', desc: 'MCP + Skill + Session Hook, fully automated' },
        { label: 'Standard', desc: 'one manual hook step' },
      ],
      note: 'Unknown compatible agents can be connected manually.',
    },
    quickstart: {
      title: 'Quick start',
      steps: [
        'Download the desktop app (macOS / Linux / Windows) and start the local agent',
        'Pick a connector and auto-install MCP',
        'Copy this prompt to your AI:',
      ],
      promptHint: 'prompt',
      prompt:
        'List the services running in my project, find ERROR-level logs, and tell me which service is least healthy and why.',
    },
    roadmap: {
      title: 'Open source & roadmap',
      stats: [
        { value: '75', label: 'MCP tools' },
        { value: '7', label: 'language runtimes' },
        { value: '12', label: 'pipeline templates' },
        { value: '3', label: 'desktop platforms' },
      ],
      oss: 'Apache-2.0. Local features are complete and free forever — no Open Core. Cloud (if any) sells convenience, never features.',
      items: [
        {
          title: 'Workspace Sandbox',
          desc: 'Parallel multi-agent worktree isolation (devcontainer)',
          status: 'design',
          statusLabel: 'In design',
        },
        {
          title: 'Streamable HTTP MCP transport',
          desc: '',
          status: 'research',
          statusLabel: 'Researching',
        },
        { title: 'Grok connector', desc: '', status: 'design', statusLabel: 'In design' },
        {
          title: 'v0.2.0 cross-platform desktop release',
          desc: '',
          status: 'released',
          statusLabel: 'Released',
        },
      ],
    },
    footer: {
      tagline: 'Local-first, forever.',
      links: [
        { label: 'GitHub', href: REPO },
        { label: 'Docs', href: '#' },
        { label: 'Contributing', href: `${REPO}/blob/main/CONTRIBUTING.md` },
        { label: 'Apache-2.0 License', href: `${REPO}/blob/main/LICENSE` },
      ],
    },
  },

  zh: {
    localeLabel: 'EN',
    localeHref: '/',
    nav: [
      { label: '演示', href: '#demo', optional: true },
      { label: '痛点', href: '#pains', optional: true },
      { label: '能力', href: '#capabilities' },
      { label: '安全', href: '#safety' },
      { label: '项目', href: '#projects', optional: true },
      { label: '连接器', href: '#connectors' },
      { label: '快速开始', href: '#quickstart' },
      { label: '路线图', href: '#roadmap' },
    ],
    github: 'GitHub',
    hero: {
      headline: '「帮我跑一下看看。」',
      subline: '——你的 AI 今天第几次使唤你了？',
      paragraph:
        '它能写代码，但跑不了、看不着、查不了：服务起没起要你截图，页面崩没崩要你点击，变量什么值要你复制。SuperDev 把运行时的眼睛和手还给它——跑、看、查、验、部署它自己来，动你的环境前才问你。',
      primaryCta: '下载 SuperDev v0.2.1',
      secondaryCta: '看演示',
      badges: ['Apache-2.0', 'macOS / Linux / Windows', 'local-first', 'MCP'],
      screenshotAlt: 'SuperDev 桌面端——统一运行态控制台',
    },
    demo: {
      title: '演示',
      caption: '演示视频，敬请期待。',
      videoAlt: 'SuperDev 演示占位',
    },
    pains: {
      title: '这些时刻，你一定遇到过',
      items: [
        {
          quote: '「把日志贴给我。」',
          body: '你成了 AI 的运行时接口。官方指南都在教你 paste kubectl output、截图 Grafana 喂给它。',
        },
        {
          quote: '「截个图给我看看页面。」',
          body: '它改了 UI，却要你验收。AI 看不见渲染结果，前端开发等于闭眼开车。',
        },
        {
          quote: '「断点停在第 47 行，把变量值发我。」',
          body: '推理再强，拿不到运行时现场。教程都在教人类替 AI 当调试器。',
        },
        {
          quote: '部署？还是你自己来吧。',
          body: '不是 AI 不会，是你不敢。它动你的环境从不问你，出了事全算你的。',
        },
      ],
    },
    capabilities: {
      title: '看 · 查 · 操',
      items: [
        {
          icon: 'see',
          name: '看 See',
          tagline: '统一运行态 + 日志',
          body: '本地与远端所有项目的服务、进程、端口、Docker、日志汇聚一处；AI 直接 tail / search / 取上下文，不再让你贴。75 个 MCP 工具随它调用。',
        },
        {
          icon: 'inspect',
          name: '查 Inspect',
          tagline: '断点调试',
          body: 'attach 活进程（不重启、同 pid），停在源码某一行，一次调用读回调用栈、作用域和变量。开箱即用 Go / Python / Rust / C++；实验性 Node、JVM（Java/Kotlin）。',
        },
        {
          icon: 'operate',
          name: '操 Operate',
          tagline: '浏览器 + 部署',
          body: '用你自己的 Chromium（零下载）导航、点击、输入、截图、读 console 和 network，改完前端自己验收；DAG 流水线构建、部署、回滚。',
        },
      ],
    },
    safety: {
      title: '安全门装在 agent 层，不靠提示词自觉',
      flow: [
        { name: 'preview 预检', desc: '写操作先预检' },
        { name: 'approve 桌面审批', desc: '桌面端等你点「批准」' },
        { name: '一次性 token', desc: '指纹绑定 · 限时 · 单次' },
        { name: 'audit 全程审计', desc: '操作留痕可回溯' },
      ],
      bullets: [
        '写操作要么直接调用、要么在桌面端等你点“批准”，MCP 自动挂起、凭一次性 token 恢复执行。debug_evaluate 按表达式审计，浏览器 evaluate 走授信开关，控制动作脱敏记录。',
        '调试凭据：AI 遇到登录墙，用你预置的测试凭据合法登录——它永远不需要伪造 token。',
      ],
    },
    projects: {
      title: '一个桌面，管住所有项目',
      items: [
        {
          title: '本地 + 远端统一模型',
          body: 'SSH 导入远端主机，进程 / launchd / systemd / Docker / nginx 静态站点同一份事实源，桌面端和 AI 看到的是同一个世界。',
        },
        {
          title: '12 个内置流水线模板',
          body: 'Go / Node / Python / Java / Rust / PHP / Vue+Go…构建、产物、部署、回滚可重放。',
        },
        {
          title: '声明式 Ingress',
          body: '域名、DNS（Cloudflare / 阿里云 / 手动）、nginx 反代、ACME DNS-01 证书，状态收敛而非脚本堆砌。',
        },
      ],
    },
    connectors: {
      title: '接上你正在用的 AI',
      lead: '不用换掉你的 AI，给它补全后半程。七个内置连接器，一次点击自动装好 MCP + Skill：',
      chips: ['Claude Code', 'Codex', 'Cursor', 'OpenCode', 'OpenClaw', 'Hermes', 'Kimi Code'],
      tiers: [
        { label: 'Full 级', desc: 'MCP + Skill + Session Hook 全自动' },
        { label: 'Standard 级', desc: 'Hook 手动一步' },
      ],
      note: '未知兼容 Agent 支持手动接入。',
    },
    quickstart: {
      title: '快速开始',
      steps: [
        '下载桌面端（macOS / Linux / Windows），启动本地 agent',
        '选一个连接器，自动安装 MCP',
        '复制这段 prompt 给你的 AI：',
      ],
      promptHint: 'prompt',
      prompt: '看看我的项目里有哪些服务在跑，把 ERROR 级别的日志找出来，告诉我哪个服务最不健康，为什么。',
    },
    roadmap: {
      title: '开源与路线图',
      stats: [
        { value: '75', label: 'MCP 工具' },
        { value: '7', label: '种语言运行时' },
        { value: '12', label: '个流水线模板' },
        { value: '3', label: '个桌面平台' },
      ],
      oss: 'Apache-2.0；本地功能永远完整免费，不做 Open Core；云端（如有）只卖便利，不卖功能。',
      items: [
        {
          title: 'Workspace Sandbox',
          desc: '多 Agent 并行 worktree 隔离（devcontainer）',
          status: 'design',
          statusLabel: '设计中',
        },
        { title: 'Streamable HTTP MCP transport', desc: '', status: 'research', statusLabel: '研究中' },
        { title: 'Grok 连接器', desc: '', status: 'design', statusLabel: '设计中' },
        { title: 'v0.2.0 跨平台桌面发布', desc: '', status: 'released', statusLabel: '已发布' },
      ],
    },
    footer: {
      tagline: '本地优先，永远如此。',
      links: [
        { label: 'GitHub', href: REPO },
        { label: '文档 Docs', href: '#' },
        { label: '贡献指南', href: `${REPO}/blob/main/CONTRIBUTING.md` },
        { label: 'Apache-2.0 License', href: `${REPO}/blob/main/LICENSE` },
      ],
    },
  },
}

export const RELEASES_URL = `${REPO}/releases`
