/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { CherryStudio } from '@lobehub/icons'
import { Link } from '@tanstack/react-router'
import { ArrowRight, BookOpen } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { useStatus } from '@/hooks/use-status'

import { HeroTerminalDemo } from '../hero-terminal-demo'

interface HeroProps {
  className?: string
  isAuthenticated?: boolean
}

// Stylized three-dots indicator representing "More"
const MoreIcon = () => (
  <svg
    className='text-muted-foreground/60 group-hover:text-foreground size-6 shrink-0 transition-colors'
    viewBox='0 0 24 24'
    fill='none'
    xmlns='http://www.w3.org/2000/svg'
  >
    <circle cx='6' cy='12' r='2' fill='currentColor' />
    <circle cx='12' cy='12' r='2' fill='currentColor' />
    <circle cx='18' cy='12' r='2' fill='currentColor' />
  </svg>
)

export function Hero(props: HeroProps) {
  const { t } = useTranslation()
  const { status } = useStatus()
  const docsUrl =
    (status?.docs_link as string | undefined) || 'https://docs.newapi.pro'

  const renderDocsButton = () => {
    const isExternal = docsUrl.startsWith('http')
    if (isExternal) {
      return (
        <Button
          variant='outline'
          className='group border-border bg-background text-foreground hover:border-primary/30 hover:bg-primary/5 h-11 px-5 text-sm font-medium'
          render={
            <a href={docsUrl} target='_blank' rel='noopener noreferrer' />
          }
        >
          <BookOpen className='text-muted-foreground group-hover:text-foreground size-4 transition-colors duration-200' />
          <span>{t('Docs')}</span>
        </Button>
      )
    }
    return (
      <Button
        variant='outline'
        className='group border-border bg-background text-foreground hover:border-primary/30 hover:bg-primary/5 h-11 px-5 text-sm font-medium'
        render={<Link to={docsUrl} />}
      >
        <BookOpen className='text-muted-foreground group-hover:text-foreground size-4 transition-colors duration-200' />
        <span>{t('Docs')}</span>
      </Button>
    )
  }

  return (
    <section className='bg-background text-foreground relative z-10 min-h-[calc(100svh-3rem)] overflow-hidden px-6 pt-28 pb-14 md:pt-36 md:pb-20 lg:pt-40 lg:pb-24'>
      <div
        aria-hidden
        className='hero-light-field pointer-events-none absolute -top-48 right-[8%] -z-10 h-[42rem] w-[46rem]'
      />
      <div
        aria-hidden
        className='hero-scanline pointer-events-none absolute inset-x-0 top-0 -z-10'
      />
      <div
        aria-hidden
        className='hero-grid-drift pointer-events-none absolute inset-0 -z-10 bg-[linear-gradient(to_right,color-mix(in_oklch,var(--foreground)_5%,transparent)_1px,transparent_1px),linear-gradient(to_bottom,color-mix(in_oklch,var(--foreground)_5%,transparent)_1px,transparent_1px)] [mask-image:linear-gradient(to_bottom,black,transparent_90%)] bg-[size:5rem_5rem]'
      />
      <div
        aria-hidden
        className='bg-primary absolute inset-x-0 top-0 -z-10 h-1'
      />

      <div className='mx-auto grid max-w-7xl grid-cols-1 items-center gap-14 lg:grid-cols-12 lg:gap-14'>
        {/* Left Column: Title, description, action buttons and application support */}
        <div className='flex flex-col items-start text-left lg:col-span-6'>
          {/* Top Pill Badge */}
          <div
            className='hero-badge landing-animate-fade-up border-primary text-primary mb-7 inline-flex items-center gap-2 border-l-2 pl-3 text-xs font-semibold opacity-0'
            style={{ animationDelay: '0ms' }}
          >
            <span className='relative flex size-1.5'>
              <span className='bg-primary absolute inline-flex h-full w-full animate-ping rounded-full opacity-60' />
              <span className='bg-primary relative inline-flex size-1.5 rounded-full' />
            </span>
            <span>{t('AI Application Infrastructure Foundation')}</span>
          </div>

          <h1
            className='landing-animate-fade-up max-w-3xl text-[clamp(2.75rem,5vw,4.75rem)] leading-[1.04] font-semibold tracking-normal'
            style={{ animationDelay: '60ms' }}
          >
            {t('Unified API Gateway for')}
            <br />
            <span className='hero-title-sheen text-primary'>
              {t('Vast Range of AI Models')}
            </span>
          </h1>
          <p
            className='text-muted-foreground landing-animate-fade-up mt-7 max-w-xl text-base leading-7 opacity-0 md:text-lg'
            style={{ animationDelay: '120ms' }}
          >
            {t(
              'Access a vast selection of models via a standard, unified API protocol. Power AI applications, manage digital assets, and connect the Future.'
            )}
          </p>

          <div
            className='landing-animate-fade-up mt-8 flex flex-wrap items-center gap-3 opacity-0'
            style={{ animationDelay: '180ms' }}
          >
            {props.isAuthenticated ? (
              <>
                <Button
                  className='group bg-primary text-primary-foreground hover:bg-primary/90 h-11 px-5 text-sm font-semibold'
                  render={<Link to='/dashboard' />}
                >
                  {t('Go to Dashboard')}
                  <ArrowRight className='ml-1.5 size-4 transition-transform duration-200 group-hover:translate-x-0.5' />
                </Button>
                {renderDocsButton()}
              </>
            ) : (
              <>
                <Button
                  className='group bg-primary text-primary-foreground hover:bg-primary/90 h-11 px-5 text-sm font-semibold'
                  render={<Link to='/sign-up' />}
                >
                  {t('Get Started')}
                  <ArrowRight className='ml-1.5 size-4 transition-transform duration-200 group-hover:translate-x-0.5' />
                </Button>
                <Button
                  variant='outline'
                  className='border-border bg-background text-foreground hover:border-primary/30 hover:bg-primary/5 h-11 px-5 text-sm font-medium'
                  render={<Link to='/pricing' />}
                >
                  {t('View Pricing')}
                </Button>
                {renderDocsButton()}
              </>
            )}
          </div>

          {/* Supported Apps (参考图二样式，进行卡片化和信息扩充设计，增加视觉高度) */}
          <div
            className='landing-animate-fade-up border-border mt-12 w-full max-w-xl border-t pt-6 opacity-0'
            style={{ animationDelay: '240ms' }}
          >
            <div className='mb-4 flex flex-col gap-1'>
              <span className='text-foreground text-xs font-semibold uppercase'>
                {t('Supported Applications')}
              </span>
              <p className='text-muted-foreground text-sm leading-relaxed'>
                {t(
                  'Supports one-click configuration and perfectly adapts to NewAPI multi-protocol configuration.'
                )}
              </p>
            </div>
            <div className='flex flex-wrap items-center gap-3'>
              {/* Cherry Studio */}
              <a
                href='https://cherry-ai.com'
                target='_blank'
                rel='noopener noreferrer'
                className='group border-border bg-card text-foreground hover:border-primary/30 hover:bg-primary/5 flex items-center gap-3 rounded-lg border px-4 py-2.5 text-sm font-medium transition-colors'
              >
                <CherryStudio.Color size={24} className='shrink-0' />
                <span>Cherry Studio</span>
              </a>

              {/* CC Switch */}
              <a
                href='https://ccswitch.io'
                target='_blank'
                rel='noopener noreferrer'
                className='group border-border bg-card text-foreground hover:border-primary/30 hover:bg-primary/5 flex items-center gap-3 rounded-lg border px-4 py-2.5 text-sm font-medium transition-colors'
              >
                <img
                  src='https://ccswitch.io/favicon.png'
                  alt='CC Switch'
                  className='size-6 shrink-0 rounded-md object-contain'
                  onError={(e) => {
                    // Fallback to a styled text avatar if the remote favicon fails to load in sandbox or local environments
                    e.currentTarget.style.display = 'none'
                    const fallback = e.currentTarget.nextSibling as HTMLElement
                    if (fallback) fallback.style.display = 'flex'
                  }}
                />
                <span
                  style={{ display: 'none' }}
                  className='size-6 shrink-0 items-center justify-center rounded-md bg-blue-500/10 text-[10px] font-bold text-blue-600 dark:bg-blue-400/10 dark:text-blue-400'
                >
                  CC
                </span>
                <span>CC Switch</span>
              </a>

              {/* "更多" */}
              <div className='text-muted-foreground group border-border bg-card hover:border-primary/30 hover:bg-primary/5 hover:text-foreground flex cursor-default items-center gap-2.5 rounded-lg border px-4 py-2.5 text-sm font-medium transition-colors'>
                <MoreIcon />
                <span>{t('More Apps')}</span>
              </div>
            </div>
          </div>
        </div>

        {/* Right Column: Hero Terminal API Demo */}
        <div
          className='landing-animate-fade-up relative flex w-full justify-center opacity-0 lg:col-span-6'
          style={{ animationDelay: '320ms' }}
        >
          <div
            aria-hidden
            className='hero-terminal-frame border-primary/10 pointer-events-none absolute -inset-5 rounded-[2rem] border'
          />
          <div
            aria-hidden
            className='hero-terminal-telemetry text-muted-foreground pointer-events-none absolute -top-6 right-2 hidden font-mono text-[9px] tracking-[0.22em] uppercase sm:block'
          >
            SYS / 01&nbsp;&nbsp;|&nbsp;&nbsp;ROUTE / LIVE
          </div>
          <div className='hero-terminal-float relative w-full'>
            <HeroTerminalDemo className='mt-8 lg:mt-0' />
          </div>
          <div
            aria-hidden
            className='hero-status-rail text-muted-foreground pointer-events-none absolute -bottom-10 left-1/2 hidden w-[86%] -translate-x-1/2 items-center justify-between gap-4 px-4 py-2 font-mono text-[9px] tracking-[0.16em] uppercase sm:flex'
          >
            <span>
              <i className='hero-status-dot' /> API / READY
            </span>
            <span>EDGE ROUTING</span>
            <span>LATENCY 142MS</span>
          </div>
        </div>
      </div>
    </section>
  )
}
