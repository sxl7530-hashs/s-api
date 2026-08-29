export const SITE_DESCRIPTION =
  '统一接入 OpenAI、Claude、Gemini、DeepSeek 等主流模型的 AI API 网关，支持 OpenAI 兼容接口、模型聚合、故障切换和统一计费。'

export function pageTitle(title: string): string {
  return `${title} - New API`
}

export function seoHead(
  title: string,
  description = SITE_DESCRIPTION,
  canonical = '/'
) {
  return {
    meta: [
      { title: pageTitle(title) },
      { name: 'description', content: description },
      { name: 'robots', content: 'index,follow' },
      { property: 'og:type', content: 'website' },
      { property: 'og:title', content: pageTitle(title) },
      { property: 'og:description', content: description },
      { property: 'og:url', content: `https://viralapi.ai${canonical}` },
      { property: 'og:image', content: 'https://viralapi.ai/logo.png' },
      { name: 'twitter:card', content: 'summary' },
      { name: 'twitter:title', content: pageTitle(title) },
      { name: 'twitter:description', content: description },
    ],
    links: [{ rel: 'canonical', href: canonical }],
  }
}

export function modelSeoHead(modelId: string) {
  const title = `${modelId} API 价格与接入指南`
  const canonical = `/pricing/${encodeURIComponent(modelId)}/`
  const description = `查看 ${modelId} API 的价格、可用分组、模型能力与统一调用方式，支持 OpenAI 兼容接口和多渠道接入。`
  return {
    ...seoHead(title, description, canonical),
    scripts: [
      {
        type: 'application/ld+json',
        children: JSON.stringify({
          '@context': 'https://schema.org',
          '@type': 'TechArticle',
          headline: pageTitle(title),
          description,
          url: `https://viralapi.ai${canonical}`,
          author: { '@type': 'Organization', name: 'New API' },
          publisher: { '@type': 'Organization', name: 'New API' },
          about: { '@type': 'SoftwareApplication', name: modelId },
        }),
      },
      {
        type: 'application/ld+json',
        children: JSON.stringify({
          '@context': 'https://schema.org',
          '@type': 'BreadcrumbList',
          itemListElement: [
            { '@type': 'ListItem', position: 1, name: '首页', item: 'https://viralapi.ai/' },
            { '@type': 'ListItem', position: 2, name: '模型价格', item: 'https://viralapi.ai/pricing/' },
            { '@type': 'ListItem', position: 3, name: modelId, item: `https://viralapi.ai${canonical}` },
          ],
        }),
      },
    ],
  }
}

export const noIndexHead = {
  meta: [{ name: 'robots', content: 'noindex,nofollow,noarchive' }],
}
