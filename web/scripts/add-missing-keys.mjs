import fs from 'node:fs/promises'
import path from 'node:path'

const dir = path.resolve('src/i18n/locales')
const newKeys = {
  en: {
    'Cache Critical Watermark (%)': 'Cache Critical Watermark (%)',
    'Reject new large requests after this cache usage': 'Reject new large requests after this cache usage',
    'Max Cache Per Request (MB)': 'Max Cache Per Request (MB)',
    'Set 0 for no additional per-request limit': 'Set 0 for no additional per-request limit',
    'Use Disk First for Unknown-Length Requests': 'Use Disk First for Unknown-Length Requests',
    'Auto-size cache from disk capacity': 'Auto-size cache from disk capacity',
    'Maximum disk usage for cache (%)': 'Maximum disk usage for cache (%)',
    'Automatic mode uses this percentage of the disk': 'Automatic mode uses this percentage of the disk',
    'Minimum free disk space (MB)': 'Minimum free disk space (MB)',
    'Always keep this space free': 'Always keep this space free',
  },
  zh: {
    'Cache Critical Watermark (%)': '缓存临界水位（%）',
    'Reject new large requests after this cache usage': '缓存达到此使用率后拒绝新的大请求',
    'Max Cache Per Request (MB)': '单个请求最大缓存（MB）',
    'Set 0 for no additional per-request limit': '设置为 0 表示不额外限制单个请求',
    'Use Disk First for Unknown-Length Requests': '未知长度请求优先使用磁盘',
    'Auto-size cache from disk capacity': '按磁盘容量自动计算缓存上限',
    'Maximum disk usage for cache (%)': '缓存最多使用磁盘比例（%）',
    'Automatic mode uses this percentage of the disk': '自动模式按此比例使用磁盘',
    'Minimum free disk space (MB)': '最低剩余磁盘空间（MB）',
    'Always keep this space free': '始终保留这部分空间',
  },
  'zh-TW': {
    'Cache Critical Watermark (%)': '快取臨界水位（%）',
    'Reject new large requests after this cache usage': '快取達到此使用率後拒絕新的大型請求',
    'Max Cache Per Request (MB)': '單一請求最大快取（MB）',
    'Set 0 for no additional per-request limit': '設為 0 表示不額外限制單一請求',
    'Use Disk First for Unknown-Length Requests': '未知長度請求優先使用磁碟',
  },
  fr: {
    'Cache Critical Watermark (%)': 'Seuil critique du cache (%)',
    'Reject new large requests after this cache usage': 'Refuser les nouvelles requêtes volumineuses après ce seuil',
    'Max Cache Per Request (MB)': 'Cache maximal par requête (Mo)',
    'Set 0 for no additional per-request limit': '0 signifie aucune limite supplémentaire par requête',
    'Use Disk First for Unknown-Length Requests': 'Utiliser le disque en priorité pour les requêtes de longueur inconnue',
  },
  ja: {
    'Cache Critical Watermark (%)': 'キャッシュ臨界水位（%）',
    'Reject new large requests after this cache usage': 'この使用率に達したら新しい大規模リクエストを拒否',
    'Max Cache Per Request (MB)': 'リクエストごとの最大キャッシュ（MB）',
    'Set 0 for no additional per-request limit': '0 はリクエスト単位の追加制限なし',
    'Use Disk First for Unknown-Length Requests': '長さ不明のリクエストはディスクを優先',
  },
  ru: {
    'Cache Critical Watermark (%)': 'Критический порог кэша (%)',
    'Reject new large requests after this cache usage': 'Отклонять новые большие запросы после этого порога',
    'Max Cache Per Request (MB)': 'Максимальный кэш на запрос (МБ)',
    'Set 0 for no additional per-request limit': '0 — без дополнительного ограничения на запрос',
    'Use Disk First for Unknown-Length Requests': 'Для запросов неизвестной длины сначала использовать диск',
  },
  vi: {
    'Cache Critical Watermark (%)': 'Ngưỡng bộ nhớ đệm (%)',
    'Reject new large requests after this cache usage': 'Từ chối yêu cầu lớn mới khi đạt mức sử dụng này',
    'Max Cache Per Request (MB)': 'Bộ nhớ đệm tối đa mỗi yêu cầu (MB)',
    'Set 0 for no additional per-request limit': 'Đặt 0 để không giới hạn bổ sung cho mỗi yêu cầu',
    'Use Disk First for Unknown-Length Requests': 'Ưu tiên đĩa cho yêu cầu không biết độ dài',
  },
}
for (const locale of Object.keys(newKeys)) {
  const file = path.join(dir, `${locale}.json`)
  const data = JSON.parse(await fs.readFile(file, 'utf8'))
  for (const [key, value] of Object.entries(newKeys[locale])) data.translation[key] = value
  await fs.writeFile(file, `${JSON.stringify(data, null, 2)}\n`)
}
