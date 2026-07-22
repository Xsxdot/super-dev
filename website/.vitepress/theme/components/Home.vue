<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useData, withBase } from 'vitepress'
import { homeCopy, RELEASES_URL } from '../home-copy'

const REPO = 'https://github.com/Xsxdot/super-dev'

const { lang, isDark } = useData()
const isZh = computed(() => lang.value.startsWith('zh'))
const t = computed(() => homeCopy[isZh.value ? 'zh' : 'en'])

const logoSrc = withBase('/logo.svg')
const screenshotSrc = withBase('/screenshot-en.png')
const localeHref = computed(() => withBase(t.value.localeHref))

function toggleDark() {
  isDark.value = !isDark.value
}

/** scroll reveal: transform/opacity only; hidden state applied only after JS ready */
const jsReady = ref(false)
let io: IntersectionObserver | null = null

onMounted(() => {
  jsReady.value = true
  io = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (entry.isIntersecting) {
          entry.target.classList.add('sd-inview')
          io?.unobserve(entry.target)
        }
      }
    },
    { threshold: 0.08, rootMargin: '0px 0px -32px 0px' },
  )
  document.querySelectorAll('.sd-reveal').forEach((el) => io!.observe(el))
})

onBeforeUnmount(() => {
  io?.disconnect()
  io = null
})

const delay = (i: number) => ({ '--sd-delay': `${(i % 4) * 60}ms` } as Record<string, string>)
</script>

<template>
  <div id="top" class="sd-home" :class="{ 'sd-js': jsReady }">
    <!-- ============ Header ============ -->
    <header class="sd-header" role="banner">
      <div class="sd-header-inner">
        <a class="sd-header-logo" href="#top" aria-label="SuperDev">
          <img :src="logoSrc" alt="SuperDev logo" width="28" height="28" />
          <span>SuperDev</span>
        </a>

        <nav class="sd-header-nav" role="navigation" :aria-label="isZh ? '主导航' : 'Main navigation'">
          <a
            v-for="item in t.nav"
            :key="item.href"
            class="sd-nav-link"
            :class="{ 'sd-nav-link--optional': item.optional }"
            :href="item.href"
            >{{ item.label }}</a
          >
        </nav>

        <div class="sd-header-right">
          <a class="sd-locale-link" :href="localeHref" :lang="isZh ? 'en' : 'zh-CN'">{{ t.localeLabel }}</a>
          <button
            class="sd-theme-toggle"
            type="button"
            :aria-label="isDark ? (isZh ? '切换到浅色模式' : 'Switch to light mode') : isZh ? '切换到深色模式' : 'Switch to dark mode'"
            @click="toggleDark"
          >
            <svg v-if="isDark" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" aria-hidden="true">
              <circle cx="12" cy="12" r="4" />
              <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41" />
            </svg>
            <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
            </svg>
          </button>
          <a class="sd-btn sd-btn--primary sd-btn--32" :href="REPO" target="_blank" rel="noopener">
            <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true" style="width: 16px; height: 16px">
              <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z" />
            </svg>
            {{ t.github }}
          </a>
        </div>
      </div>
    </header>

    <main>
      <!-- ============ Hero ============ -->
      <section class="sd-hero">
        <div class="sd-container">
          <img class="sd-hero-logo sd-reveal" :src="logoSrc" alt="" aria-hidden="true" />
          <h1 class="sd-reveal" :style="delay(1)">{{ t.hero.headline }}</h1>
          <p class="sd-hero-subline sd-reveal" :style="delay(2)">{{ t.hero.subline }}</p>
          <p class="sd-hero-paragraph sd-reveal" :style="delay(3)">{{ t.hero.paragraph }}</p>
          <div class="sd-hero-ctas sd-reveal" :style="delay(0)">
            <a class="sd-btn sd-btn--primary sd-btn--44" :href="RELEASES_URL" target="_blank" rel="noopener">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" style="width: 20px; height: 20px">
                <path d="M12 3v12M7 10l5 5 5-5M4 21h16" />
              </svg>
              {{ t.hero.primaryCta }}
            </a>
            <a class="sd-btn sd-btn--outline sd-btn--44" href="#demo">{{ t.hero.secondaryCta }}</a>
          </div>
          <div class="sd-hero-badges sd-reveal" :style="delay(1)">
            <span v-for="b in t.hero.badges" :key="b" class="sd-badge">{{ b }}</span>
          </div>
          <div class="sd-hero-screenshot sd-reveal" :style="delay(2)">
            <img :src="screenshotSrc" :alt="t.hero.screenshotAlt" />
          </div>
        </div>
      </section>

      <!-- ============ Demo ============ -->
      <section id="demo" class="sd-section sd-section--alt">
        <div class="sd-container">
          <div class="sd-section-head sd-reveal">
            <h2 class="sd-section-title">{{ t.demo.title }}</h2>
          </div>
          <div class="sd-demo-frame sd-reveal" :style="delay(1)" role="img" :aria-label="t.demo.videoAlt">
            <img :src="screenshotSrc" :alt="t.demo.videoAlt" />
            <div class="sd-demo-play">
              <span class="sd-demo-play-icon" aria-hidden="true">
                <svg viewBox="0 0 24 24" fill="currentColor"><path d="M8 5.14v13.72c0 .8.87 1.3 1.56.88l10.5-6.86a1.03 1.03 0 0 0 0-1.76L9.56 4.26A1.03 1.03 0 0 0 8 5.14Z" /></svg>
              </span>
              <span class="sd-demo-caption">{{ t.demo.caption }}</span>
            </div>
          </div>
        </div>
      </section>

      <!-- ============ Pains ============ -->
      <section id="pains" class="sd-section">
        <div class="sd-container">
          <div class="sd-section-head sd-reveal">
            <h2 class="sd-section-title">{{ t.pains.title }}</h2>
          </div>
          <div class="sd-pain-grid">
            <article v-for="(p, i) in t.pains.items" :key="p.quote" class="sd-card sd-reveal" :style="delay(i)">
              <p class="sd-pain-quote">{{ p.quote }}</p>
              <p class="sd-pain-body">{{ p.body }}</p>
            </article>
          </div>
        </div>
      </section>

      <!-- ============ Capabilities ============ -->
      <section id="capabilities" class="sd-section sd-section--alt">
        <div class="sd-container">
          <div class="sd-section-head sd-reveal">
            <h2 class="sd-section-title">{{ t.capabilities.title }}</h2>
          </div>
          <div class="sd-cap-grid">
            <article v-for="(c, i) in t.capabilities.items" :key="c.name" class="sd-card sd-reveal" :style="delay(i)">
              <span class="sd-cap-icon" aria-hidden="true">
                <svg v-if="c.icon === 'see'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7Z" />
                  <circle cx="12" cy="12" r="3" />
                </svg>
                <svg v-else-if="c.icon === 'inspect'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                  <circle cx="12" cy="12" r="9" />
                  <circle cx="12" cy="12" r="2.5" fill="currentColor" stroke="none" />
                  <path d="M12 3v3M12 18v3M3 12h3M18 12h3" />
                </svg>
                <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                  <rect x="2.5" y="4" width="19" height="16" rx="2.5" />
                  <path d="M2.5 9h19M6 6.6h.01M8.8 6.6h.01" />
                  <path d="M9 13.5 11.5 16 9 18.5M13.5 18.5H16" />
                </svg>
              </span>
              <h3 class="sd-cap-name">{{ c.name }}</h3>
              <p class="sd-cap-tagline">{{ c.tagline }}</p>
              <p class="sd-cap-body">{{ c.body }}</p>
            </article>
          </div>
        </div>
      </section>

      <!-- ============ Safety ============ -->
      <section id="safety" class="sd-section">
        <div class="sd-container">
          <div class="sd-section-head sd-reveal">
            <h2 class="sd-section-title">{{ t.safety.title }}</h2>
          </div>
          <ol class="sd-flow">
            <template v-for="(s, i) in t.safety.flow" :key="s.name">
              <li class="sd-flow-step sd-reveal" :style="delay(i)">
                <p class="sd-flow-name">{{ s.name }}</p>
                <p class="sd-flow-desc">{{ s.desc }}</p>
              </li>
              <li v-if="i < t.safety.flow.length - 1" class="sd-flow-arrow" aria-hidden="true">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M9 6l6 6-6 6" />
                </svg>
              </li>
            </template>
          </ol>
          <ul class="sd-safety-bullets sd-reveal">
            <li v-for="b in t.safety.bullets" :key="b">{{ b }}</li>
          </ul>
        </div>
      </section>

      <!-- ============ Projects ============ -->
      <section id="projects" class="sd-section sd-section--alt">
        <div class="sd-container">
          <div class="sd-section-head sd-reveal">
            <h2 class="sd-section-title">{{ t.projects.title }}</h2>
          </div>
          <div class="sd-project-grid">
            <div v-for="(p, i) in t.projects.items" :key="p.title" class="sd-project-block sd-reveal" :style="delay(i)">
              <h3 class="sd-project-title">{{ p.title }}</h3>
              <p class="sd-project-body">{{ p.body }}</p>
            </div>
          </div>
        </div>
      </section>

      <!-- ============ Connectors ============ -->
      <section id="connectors" class="sd-section">
        <div class="sd-container">
          <div class="sd-section-head sd-reveal">
            <h2 class="sd-section-title">{{ t.connectors.title }}</h2>
            <p class="sd-section-lead">{{ t.connectors.lead }}</p>
          </div>
          <div class="sd-chip-row sd-reveal">
            <span v-for="chip in t.connectors.chips" :key="chip" class="sd-chip">{{ chip }}</span>
          </div>
          <div class="sd-tier-row sd-reveal" :style="delay(1)">
            <span v-for="tier in t.connectors.tiers" :key="tier.label" class="sd-tier">
              <span class="sd-tier-label">{{ tier.label }}</span>
              {{ tier.desc }}
            </span>
          </div>
          <p class="sd-tier-note sd-reveal" :style="delay(2)">{{ t.connectors.note }}</p>
        </div>
      </section>

      <!-- ============ Quick start ============ -->
      <section id="quickstart" class="sd-section sd-section--alt">
        <div class="sd-container">
          <div class="sd-section-head sd-reveal">
            <h2 class="sd-section-title">{{ t.quickstart.title }}</h2>
          </div>
          <div class="sd-step-grid">
            <div v-for="(s, i) in t.quickstart.steps" :key="s" class="sd-step sd-reveal" :style="delay(i)">
              <span class="sd-step-num">{{ i + 1 }}</span>
              <p class="sd-step-text">{{ s }}</p>
            </div>
          </div>
          <div class="sd-prompt sd-reveal" :style="delay(3)">
            <p class="sd-prompt-label">{{ t.quickstart.promptHint }}</p>
            <code>{{ t.quickstart.prompt }}</code>
          </div>
        </div>
      </section>

      <!-- ============ Open source & roadmap ============ -->
      <section id="roadmap" class="sd-section">
        <div class="sd-container">
          <div class="sd-section-head sd-reveal">
            <h2 class="sd-section-title">{{ t.roadmap.title }}</h2>
          </div>
          <div class="sd-stats">
            <div v-for="(s, i) in t.roadmap.stats" :key="s.label" class="sd-stat sd-reveal" :style="delay(i)">
              <p class="sd-stat-value">{{ s.value }}</p>
              <p class="sd-stat-label">{{ s.label }}</p>
            </div>
          </div>
          <p class="sd-oss sd-reveal">{{ t.roadmap.oss }}</p>
          <div class="sd-roadmap-list">
            <div v-for="(item, i) in t.roadmap.items" :key="item.title" class="sd-roadmap-row sd-reveal" :style="delay(i)">
              <div>
                <p class="sd-roadmap-title">{{ item.title }}</p>
                <p v-if="item.desc" class="sd-roadmap-desc">{{ item.desc }}</p>
              </div>
              <span class="sd-status" :class="`sd-status--${item.status}`">{{ item.statusLabel }}</span>
            </div>
          </div>
        </div>
      </section>
    </main>

    <!-- ============ Footer ============ -->
    <footer class="sd-footer">
      <div class="sd-container sd-footer-inner">
        <div class="sd-footer-brand">
          <img :src="logoSrc" alt="" aria-hidden="true" />
          <p class="sd-footer-tagline">{{ t.footer.tagline }}</p>
        </div>
        <nav class="sd-footer-links" :aria-label="isZh ? '页脚导航' : 'Footer navigation'">
          <a
            v-for="l in t.footer.links"
            :key="l.label"
            :href="l.href"
            :target="l.href.startsWith('http') ? '_blank' : undefined"
            :rel="l.href.startsWith('http') ? 'noopener' : undefined"
            >{{ l.label }}</a
          >
        </nav>
      </div>
    </footer>
  </div>
</template>
