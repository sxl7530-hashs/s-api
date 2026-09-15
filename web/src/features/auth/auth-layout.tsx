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
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { Skeleton } from '@/components/ui/skeleton'
import { useSystemConfig } from '@/hooks/use-system-config'

type AuthLayoutProps = {
  children: React.ReactNode
}

export function AuthLayout({ children }: AuthLayoutProps) {
  const { t } = useTranslation()
  const { systemName, logo, loading } = useSystemConfig()

  return (
    <div className='relative min-h-svh overflow-hidden bg-[#111714] text-white'>
      <div aria-hidden className='absolute inset-x-0 top-0 z-20 h-1 bg-[linear-gradient(90deg,#36d399_0_42%,#f8cf58_42%_68%,#f27f6b_68%)]' />
      <div aria-hidden className='pointer-events-none absolute inset-0 bg-[linear-gradient(to_right,rgba(255,255,255,.045)_1px,transparent_1px),linear-gradient(to_bottom,rgba(255,255,255,.045)_1px,transparent_1px)] bg-[size:5rem_5rem] [mask-image:linear-gradient(90deg,black,transparent_68%)]' />
      <Link
        to='/'
        className='absolute top-6 left-6 z-30 flex items-center gap-2.5 transition-opacity hover:opacity-80 sm:top-8 sm:left-8'
      >
        <div className='relative h-8 w-8'>
          {loading ? (
            <Skeleton className='absolute inset-0 rounded-full' />
          ) : (
            <img
              src={logo}
              alt={t('Logo')}
              className='h-8 w-8 rounded-lg object-cover'
            />
          )}
        </div>
        {loading ? (
          <Skeleton className='h-6 w-24' />
        ) : (
          <h1 className='text-base font-semibold'>{systemName}</h1>
        )}
      </Link>

      <div className='relative z-10 grid min-h-svh lg:grid-cols-[minmax(0,1fr)_minmax(32rem,42%)]'>
        <div className='hidden items-end p-10 lg:flex xl:p-16'>
          <div className='max-w-xl pb-8'>
            <div className='mb-8 flex items-center gap-3 text-xs font-semibold text-emerald-300'>
              <span className='h-px w-10 bg-emerald-300' />
              {t('AI Application Infrastructure Foundation')}
            </div>
            <div className='space-y-3 text-[clamp(2.5rem,4.5vw,4.75rem)] leading-[1.02] font-semibold tracking-tight'>
              <div>One gateway.</div>
              <div className='text-emerald-300'>Every model.</div>
            </div>
            <div className='mt-10 grid grid-cols-3 gap-3 border-t border-white/10 pt-6'>
              {['OpenAI', 'Claude', 'Gemini'].map((provider) => (
                <div key={provider} className='rounded-lg border border-white/10 bg-white/[0.04] px-3 py-3 font-mono text-xs text-white/55'>
                  <span className='mr-2 text-emerald-300'>●</span>
                  {provider}
                </div>
              ))}
            </div>
          </div>
        </div>

        <div className='flex min-h-svh items-center bg-background px-5 pt-20 pb-8 text-foreground sm:px-10 lg:px-14 lg:pt-8'>
          <div className='mx-auto w-full max-w-md rounded-2xl border bg-card p-6 shadow-2xl shadow-black/10 sm:p-9'>
            {children}
          </div>
        </div>
      </div>
    </div>
  )
}
