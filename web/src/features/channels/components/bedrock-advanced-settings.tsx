import { RefreshCw } from 'lucide-react'
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
import { useState } from 'react'
import { type UseFormReturn, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { DateTimePicker } from '@/components/datetime-picker'
import { sideDrawerSwitchItemClassName } from '@/components/drawer-layout'
import { Button } from '@/components/ui/button'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getBedrockQuotas } from '../api'
import type { ChannelFormValues } from '../lib/channel-form'
import { parseModelsString } from '../lib/model-mapping-validation'
import type { BedrockModelQuota } from '../types'
import { BedrockModelLimitsEditor } from './bedrock-model-limits-editor'

export function BedrockAdvancedSettings(props: {
  form: UseFormReturn<ChannelFormValues>
  disabled: boolean
  channelId?: number | null
}) {
  const { t } = useTranslation()
  const [quotas, setQuotas] = useState<BedrockModelQuota[]>([])
  const [quotaRegion, setQuotaRegion] = useState('')
  const [isLoadingQuotas, setIsLoadingQuotas] = useState(false)
  const [modelEnabled, rampEnabled, backoffEnabled, models] = useWatch({
    control: props.form.control,
    name: [
      'bedrock_model_rate_limit_enabled',
      'bedrock_ramp_up_enabled',
      'bedrock_retry_backoff_enabled',
      'models',
    ],
  })

  const handleFetchQuotas = async () => {
    if (!props.channelId) return
    if (!props.form.getValues('proxy')?.trim()) {
      toast.error(
        t('Please fill in the channel proxy before querying AWS quotas')
      )
      return
    }
    setIsLoadingQuotas(true)
    try {
      const response = requireServerSuccess(
        await getBedrockQuotas(props.channelId)
      )
      setQuotas(response.data.models)
      setQuotaRegion(response.data.region)
      toast.success(t('Fetched successfully'))
    } catch (error) {
      handleServerError(error, t('Failed to query AWS quotas'))
    } finally {
      setIsLoadingQuotas(false)
    }
  }

  return (
    <div className='space-y-4'>
      {(
        [
          [
            'bedrock_model_rate_limit_enabled',
            t('Bedrock model rate limits'),
            t(
              'Use mapped upstream model names. Model limits apply in addition to the channel ceiling; unlisted models use channel limits.'
            ),
          ],
          [
            'bedrock_ramp_up_enabled',
            t('Bedrock gradual ramp-up'),
            t(
              'Increase configured RPM and TPM ceilings daily, reaching 100% on the last day. Requires channel rate protection.'
            ),
          ],
          [
            'bedrock_retry_backoff_enabled',
            t('Bedrock randomized retry backoff'),
            t(
              'Apply capped exponential jitter to upstream 429 and server-error retries. The existing retry budget still applies.'
            ),
          ],
        ] as const
      ).map(([name, label, description]) => (
        <FormField
          key={name}
          control={props.form.control}
          name={name}
          render={({ field }) => (
            <FormItem className={sideDrawerSwitchItemClassName()}>
              <div className='space-y-0.5'>
                <FormLabel>{label}</FormLabel>
                <FormDescription>{description}</FormDescription>
                <FormMessage />
              </div>
              <FormControl>
                <Switch
                  disabled={props.disabled}
                  checked={field.value}
                  onCheckedChange={(enabled) => {
                    if (
                      name === 'bedrock_ramp_up_enabled' &&
                      enabled &&
                      !props.form.getValues('bedrock_ramp_up_start_unix')
                    ) {
                      props.form.setValue(
                        'bedrock_ramp_up_start_unix',
                        Math.floor(Date.now() / 1000),
                        { shouldDirty: true }
                      )
                    }
                    field.onChange(enabled)
                  }}
                />
              </FormControl>
            </FormItem>
          )}
        />
      ))}

      {modelEnabled && (
        <FormField
          control={props.form.control}
          name='bedrock_model_limits'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Model limits')}</FormLabel>
              <FormControl>
                <BedrockModelLimitsEditor
                  value={field.value || '{}'}
                  models={parseModelsString(models || '')}
                  onChange={field.onChange}
                  disabled={props.disabled}
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
      )}

      {props.channelId && (
        <div className='border-border/60 space-y-3 rounded-md border p-3'>
          <div className='flex items-start justify-between gap-3'>
            <div className='space-y-0.5'>
              <div className='text-sm font-medium'>{t('AWS model quotas')}</div>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Read-only RPM and TPM quotas from AWS Service Quotas through this channel proxy.'
                )}
              </p>
            </div>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={handleFetchQuotas}
              disabled={props.disabled || isLoadingQuotas}
            >
              <RefreshCw
                className={
                  isLoadingQuotas ? 'mr-2 size-4 animate-spin' : 'mr-2 size-4'
                }
                aria-hidden='true'
              />
              {isLoadingQuotas ? t('Fetching...') : t('Fetch')}
            </Button>
          </div>
          {quotas.length > 0 && (
            <div className='overflow-x-auto'>
              <table className='w-full text-sm'>
                <thead>
                  <tr className='text-muted-foreground border-b text-left text-xs'>
                    <th className='py-2 pr-3 font-medium'>{t('Model')}</th>
                    <th className='py-2 pr-3 font-medium'>{t('RPM')}</th>
                    <th className='py-2 font-medium'>{t('TPM')}</th>
                  </tr>
                </thead>
                <tbody>
                  {quotas.map((quota) => (
                    <tr key={quota.model} className='border-b last:border-0'>
                      <td className='py-2 pr-3 font-mono text-xs'>
                        {quota.model}
                      </td>
                      <td className='py-2 pr-3 tabular-nums'>
                        {quota.rpm ?? '—'}
                      </td>
                      <td className='py-2 tabular-nums'>{quota.tpm ?? '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <p className='text-muted-foreground mt-2 text-xs'>
                {t('AWS region')}: {quotaRegion}
              </p>
            </div>
          )}
        </div>
      )}

      {rampEnabled && (
        <FormField
          control={props.form.control}
          name='bedrock_ramp_up_start_unix'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Ramp-up start time')}</FormLabel>
              <fieldset
                disabled={props.disabled}
                aria-label={t('Ramp-up start time')}
              >
                <DateTimePicker
                  value={field.value ? new Date(field.value * 1000) : undefined}
                  onChange={(date) =>
                    field.onChange(date ? Math.floor(date.getTime() / 1000) : 0)
                  }
                />
              </fieldset>
              <FormMessage />
            </FormItem>
          )}
        />
      )}
      <div className='grid grid-cols-2 gap-3'>
        {(
          [
            ['bedrock_ramp_up_days', t('Ramp-up days (1–7)'), rampEnabled, 7],
            [
              'bedrock_ramp_up_initial_percent',
              t('Initial ceiling (%)'),
              rampEnabled,
              100,
            ],
            [
              'bedrock_retry_backoff_base_ms',
              t('Base backoff (ms)'),
              backoffEnabled,
              10000,
            ],
            [
              'bedrock_retry_backoff_max_ms',
              t('Maximum backoff (ms)'),
              backoffEnabled,
              10000,
            ],
          ] as const
        )
          .filter(([, , visible]) => visible)
          .map(([name, label, , maximum]) => (
            <FormField
              key={name}
              control={props.form.control}
              name={name}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{label}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      max={maximum}
                      disabled={props.disabled}
                      {...field}
                      onChange={(event) =>
                        field.onChange(Number(event.target.value))
                      }
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          ))}
      </div>
    </div>
  )
}
