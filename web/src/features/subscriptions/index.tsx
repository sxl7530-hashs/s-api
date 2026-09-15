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
import { ArrowUpRight, CreditCard, Info, ShieldCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription } from '@/components/ui/alert'

import { SubscriptionsDialogs } from './components/subscriptions-dialogs'
import { SubscriptionsPrimaryButtons } from './components/subscriptions-primary-buttons'
import {
  SubscriptionsProvider,
  useSubscriptions,
} from './components/subscriptions-provider'
import { SubscriptionsTable } from './components/subscriptions-table'

function SubscriptionsContent() {
  const { t } = useTranslation()
  const { complianceConfirmed } = useSubscriptions()

  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          {t('Subscription Management')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <div className='flex items-center gap-2'>
            <Alert variant='default' className='hidden px-3 py-2 sm:flex'>
              <Info className='h-4 w-4' />
              <AlertDescription className='text-xs'>
                {t(
                  'Stripe/Creem requires creating products on the third-party platform and entering the ID'
                )}
              </AlertDescription>
            </Alert>
            <SubscriptionsPrimaryButtons />
          </div>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='flex h-full min-h-0 flex-col gap-4 sm:gap-5'>
            <section className='relative shrink-0 overflow-hidden rounded-2xl border border-emerald-950/12 bg-[linear-gradient(rgb(15_23_20_/_0.035)_1px,transparent_1px),linear-gradient(90deg,rgb(15_23_20_/_0.035)_1px,transparent_1px),linear-gradient(115deg,#ffffff_0%,#f0faf5_100%)] bg-[size:28px_28px,28px_28px,auto] px-5 py-5 text-foreground shadow-lg shadow-emerald-950/5 sm:px-7 sm:py-6'>
              <div className='relative flex flex-col justify-between gap-6 md:flex-row md:items-end'>
                <div className='max-w-2xl'>
                  <div className='mb-3 flex items-center gap-2 text-xs font-semibold tracking-[0.18em] text-emerald-700 uppercase'>
                    <CreditCard className='size-4' />
                    {t('Subscription Management')}
                  </div>
                  <p className='max-w-xl text-sm leading-6 text-muted-foreground'>
                    {t('Stripe/Creem requires creating products on the third-party platform and entering the ID')}
                  </p>
                </div>
                <div className='grid grid-cols-2 gap-2 text-xs text-muted-foreground sm:min-w-64'>
                  <div className='rounded-lg border border-emerald-950/10 bg-white/80 p-3 shadow-xs'>
                    <ShieldCheck className='mb-2 size-4 text-emerald-700' />
                    <span>{t('Payment Channel')}</span>
                  </div>
                  <div className='rounded-lg border border-emerald-950/10 bg-white/80 p-3 shadow-xs'>
                    <ArrowUpRight className='mb-2 size-4 text-emerald-700' />
                    <span>{t('Plan Quota')}</span>
                  </div>
                </div>
              </div>
            </section>
            {!complianceConfirmed ? (
              <Alert variant='destructive' className='shrink-0 rounded-xl'>
                <AlertDescription>
                  {t(
                    'Subscription plan creation and changes are locked until the administrator confirms compliance terms in Payment Gateway settings.'
                  )}
                </AlertDescription>
              </Alert>
            ) : null}
            <div className='min-h-0 flex-1'>
              <SubscriptionsTable />
            </div>
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <SubscriptionsDialogs />
    </>
  )
}

export function Subscriptions() {
  return (
    <SubscriptionsProvider>
      <SubscriptionsContent />
    </SubscriptionsProvider>
  )
}
