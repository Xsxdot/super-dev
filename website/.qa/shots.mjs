import { chromium } from 'playwright-core'

const BASE = 'http://127.0.0.1:7100'
const shots = [
  { name: 'en-light-1280', path: '/', colorScheme: 'light', width: 1280 },
  { name: 'en-dark-1280', path: '/', colorScheme: 'dark', width: 1280 },
  { name: 'zh-light-1280', path: '/zh/', colorScheme: 'light', width: 1280 },
  { name: 'zh-dark-1280', path: '/zh/', colorScheme: 'dark', width: 1280 },
  { name: 'zh-light-390', path: '/zh/', colorScheme: 'light', width: 390 },
]

const browser = await chromium.launch({ channel: 'chrome', headless: true })

for (const s of shots) {
  const ctx = await browser.newContext({
    viewport: { width: s.width, height: 900 },
    colorScheme: s.colorScheme,
    deviceScaleFactor: 1,
  })
  const page = await ctx.newPage()
  await page.goto(BASE + s.path, { waitUntil: 'networkidle' })
  // trigger scroll-reveal for below-fold content, then let transitions finish.
  // NOTE: html has scroll-behavior:smooth, so scrollTo must use behavior:'instant'
  await page.evaluate(async () => {
    await new Promise((resolve) => {
      let y = 0
      const step = () => {
        y += 500
        window.scrollTo({ top: y, behavior: 'instant' })
        if (y < document.body.scrollHeight + 900) setTimeout(step, 80)
        else setTimeout(resolve, 600)
      }
      step()
    })
  })
  // return to top so the sticky header stitches at the page top in fullPage capture
  await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }))
  await page.waitForTimeout(1500)
  await page.screenshot({ path: `${s.name}.png`, fullPage: true })
  console.log(`saved ${s.name}.png`)
  await ctx.close()
}

await browser.close()
console.log('done')
