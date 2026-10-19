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
import { type UseFormReturn, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { DateTimePicker } from '@/components/datetime-picker'
import { sideDrawerSwitchItemClassName } from '@/components/drawer-layout'
import { JsonEditor } from '@/components/json-editor'
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

import type { ChannelFormValues } from '../lib/channel-form'

export function BedrockAdvancedSettings(props: {
  form: UseFormReturn<ChannelFormValues>
  disabled: boolean
}) {
  const { t } = useTranslation()
  const [modelEnabled, rampEnabled, backoffEnabled] = useWatch({
    control: props.form.control,
    name: [
      'bedrock_model_rate_limit_enabled',
      'bedrock_ramp_up_enabled',
      'bedrock_retry_backoff_enabled',
    ],
  })

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
                <JsonEditor
                  value={field.value || '{}'}
                  onChange={field.onChange}
                  disabled={props.disabled}
                  valueType='any'
                  keyLabel={t('Model')}
                  valueLabel={t('RPM / TPM')}
                  template={{ 'claude-opus-4-6': { rpm: 30, tpm: 60000 } }}
                  valuePlaceholder='{"rpm":30,"tpm":60000}'
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
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
