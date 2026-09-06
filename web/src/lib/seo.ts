export const SITE_DESCRIPTION =
  '统一接入 OpenAI、Claude、Gemini、DeepSeek 等主流模型的 AI API 网关，支持 OpenAI 兼容接口、模型聚合、故障切换和统一计费。'

export const SITE_URL = 'https://viralapi.ai'

function absoluteUrl(path: string): string {
  return new URL(path, `${SITE_URL}/`).toString()
}

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
      { property: 'og:url', content: absoluteUrl(canonical) },
      { property: 'og:site_name', content: 'New API' },
      { property: 'og:locale', content: 'zh_CN' },
      { property: 'og:image', content: absoluteUrl('/logo.png') },
      { name: 'twitter:card', content: 'summary' },
      { name: 'twitter:title', content: pageTitle(title) },
      { name: 'twitter:description', content: description },
      { name: 'twitter:image', content: absoluteUrl('/logo.png') },
    ],
    links: [{ rel: 'canonical', href: absoluteUrl(canonical) }],
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
          url: absoluteUrl(canonical),
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
            {
              '@type': 'ListItem',
              position: 1,
              name: '首页',
              item: absoluteUrl('/'),
            },
            {
              '@type': 'ListItem',
              position: 2,
              name: '模型价格',
              item: absoluteUrl('/pricing/'),
            },
            {
              '@type': 'ListItem',
              position: 3,
              name: modelId,
              item: absoluteUrl(canonical),
            },
          ],
        }),
      },
    ],
  }
}

export const noIndexHead = {
  meta: [{ name: 'robots', content: 'noindex,nofollow,noarchive' }],
}
