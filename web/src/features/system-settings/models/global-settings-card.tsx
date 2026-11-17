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
import { zodResolver } from '@hookform/resolvers/zod'
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { JsonCodeEditor } from '@/components/json-code-editor'
import { StatusBadge } from '@/components/status-badge'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import { Claude5CompatSettings } from './claude5-compat-settings'

const thinkingBlacklistExample = JSON.stringify(
  ['moonshotai/kimi-k2-thinking', 'kimi-k2-thinking', 're:.*@sha256:.*'],
  null,
  2
)

const chatToResponsesPolicyExample = JSON.stringify(
  {
    enabled: true,
    all_channels: false,
    channel_ids: [1, 2],
    model_patterns: ['^gpt-4o.*$', '^gpt-5.*$'],
  },
  null,
  2
)

const chatToResponsesPolicyAllChannelsExample = JSON.stringify(
  {
    enabled: true,
    all_channels: true,
    model_patterns: ['^gpt-4o.*$', '^gpt-5.*$'],
  },
  null,
  2
)

const jsonString = z.string().refine((value) => {
  const trimmed = value.trim()
  if (!trimmed) return true
  try {
    JSON.parse(trimmed)
    return true
  } catch {
    return false
  }
}, 'Invalid JSON format')

const schema = z.object({
  global: z.object({
    aws_passthrough_compatibility_enabled: z.boolean(),
    pass_through_request_enabled: z.boolean(),
    aws_beta_filter_enabled: z.boolean(),
    aws_beta_blacklist: jsonString,
    aws_context_management_auto_beta: z.boolean(),
    aws_context_management_beta_models: jsonString,
    thinking_model_blacklist: jsonString,
    chat_completions_to_responses_policy: jsonString,
    azure_force_background_enabled: z.boolean(),
    aws_usage_metrics_enabled: z.boolean(),
    claude5_compat: jsonString,
  }),
  general_setting: z.object({
    ping_interval_enabled: z.boolean(),
    ping_interval_seconds: z.coerce.number().min(1),
  }),
})

type GlobalModelSettingsFormValues = z.output<typeof schema>
type GlobalModelSettingsFormInput = z.input<typeof schema>

type FlatGlobalModelSettings = {
  'global.aws_passthrough_compatibility_enabled': boolean
  'global.pass_through_request_enabled': boolean
  'global.aws_beta_filter_enabled': boolean
  'global.aws_beta_blacklist': string
  'global.aws_context_management_auto_beta': boolean
  'global.aws_context_management_beta_models': string
  'global.thinking_model_blacklist': string
  'global.chat_completions_to_responses_policy': string
  'global.azure_force_background_enabled': boolean
  'global.aws_usage_metrics_enabled': boolean
  'global.claude5_compat': string
  'general_setting.ping_interval_enabled': boolean
  'general_setting.ping_interval_seconds': number
}

const flattenGlobalValues = (
  values: GlobalModelSettingsFormValues
): FlatGlobalModelSettings => ({
  'global.aws_passthrough_compatibility_enabled':
    values.global.aws_passthrough_compatibility_enabled,
  'global.pass_through_request_enabled':
    values.global.pass_through_request_enabled,
  'global.aws_beta_filter_enabled': values.global.aws_beta_filter_enabled,
  'global.aws_beta_blacklist': normalizeJsonText(
    values.global.aws_beta_blacklist,
    '[]'
  ),
  'global.aws_context_management_auto_beta':
    values.global.aws_context_management_auto_beta,
  'global.aws_context_management_beta_models': normalizeJsonText(
    values.global.aws_context_management_beta_models,
    '["*claude-haiku-4-5*", "*claude-sonnet-4-5*", "*claude-opus-4-6*", "*claude-opus-4-7*", "*claude-opus-4-8*", "*claude-sonnet-4-6*", "*claude-opus-5*", "*claude-sonnet-5*", "*claude-haiku-5*", "*claude-fable-5*", "*claude-mythos-5*"]'
  ),
  'global.thinking_model_blacklist': normalizeJsonText(
    values.global.thinking_model_blacklist,
    '[]'
  ),
  'global.chat_completions_to_responses_policy': normalizeJsonText(
    values.global.chat_completions_to_responses_policy,
    '{}'
  ),
  'global.azure_force_background_enabled':
    values.global.azure_force_background_enabled,
  'global.aws_usage_metrics_enabled': values.global.aws_usage_metrics_enabled,
  'global.claude5_compat': normalizeJsonText(
    values.global.claude5_compat,
    '{}'
  ),
  'general_setting.ping_interval_enabled':
    values.general_setting.ping_interval_enabled,
  'general_setting.ping_interval_seconds':
    values.general_setting.ping_interval_seconds,
})

function normalizeJsonText(value: string, fallback: string) {
  const trimmed = (value ?? '').toString().trim()
  return trimmed ? trimmed : fallback
}

type GlobalSettingsCardProps = {
  defaultValues: GlobalModelSettingsFormValues
}

export function GlobalSettingsCard({ defaultValues }: GlobalSettingsCardProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const form = useForm<
    GlobalModelSettingsFormInput,
    unknown,
    GlobalModelSettingsFormValues
  >({
    resolver: zodResolver(schema),
    defaultValues: defaultValues as GlobalModelSettingsFormInput,
  })

  useEffect(() => {
    form.reset(defaultValues as GlobalModelSettingsFormInput)
  }, [defaultValues, form])

  const pingEnabled = form.watch('general_setting.ping_interval_enabled')
  const onSubmit = async (values: GlobalModelSettingsFormValues) => {
    const flattenedDefaults = flattenGlobalValues(defaultValues)
    const flattenedValues = flattenGlobalValues(values)
    const updates = Object.entries(flattenedValues).filter(
      ([key, value]) =>
        value !== flattenedDefaults[key as keyof FlatGlobalModelSettings]
    )

    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }

    for (const [key, value] of updates) {
      await updateOption.mutateAsync({
        key,
        value,
      })
    }
  }

  return (
    <SettingsSection title={t('Global Model Configuration')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
          />
          <FormField
            control={form.control}
            name='global.pass_through_request_enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable Request Passthrough')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Forward requests directly to upstream providers without any post-processing.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name='global.claude5_compat'
            render={({ field }) => (
              <FormItem className='space-y-3'>
                <Claude5CompatSettings
                  value={field.value}
                  onChange={field.onChange}
                  awsContextManagementAutoBeta={form.watch(
                    'global.aws_context_management_auto_beta'
                  )}
                  onAwsContextManagementAutoBetaChange={(value) =>
                    form.setValue(
                      'global.aws_context_management_auto_beta',
                      value,
                      {
                        shouldDirty: true,
                      }
                    )
                  }
                  awsContextManagementBetaModels={form.watch(
                    'global.aws_context_management_beta_models'
                  )}
                  onAwsContextManagementBetaModelsChange={(value) =>
                    form.setValue(
                      'global.aws_context_management_beta_models',
                      value,
                      { shouldDirty: true, shouldValidate: true }
                    )
                  }
                  awsContextManagementBetaModelsError={
                    form.formState.errors.global
                      ?.aws_context_management_beta_models?.message
                  }
                  awsBetaFilterEnabled={form.watch(
                    'global.aws_beta_filter_enabled'
                  )}
                  onAwsBetaFilterEnabledChange={(value) =>
                    form.setValue('global.aws_beta_filter_enabled', value, {
                      shouldDirty: true,
                    })
                  }
                  awsBetaBlacklist={form.watch('global.aws_beta_blacklist')}
                  onAwsBetaBlacklistChange={(value) =>
                    form.setValue('global.aws_beta_blacklist', value, {
                      shouldDirty: true,
                      shouldValidate: true,
                    })
                  }
                  awsBetaBlacklistError={
                    form.formState.errors.global?.aws_beta_blacklist?.message
                  }
                />
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='global.aws_passthrough_compatibility_enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>
                    {t('AWS Bedrock Passthrough Compatibility')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'Convert supported client requests when AWS passthrough is enabled. Users with AWS direct passthrough bypass compatibility. Disabled by default.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name='global.aws_usage_metrics_enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable AWS RPM/TPM Metrics')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Collect rolling RPM and TPM for all AWS Bedrock channels.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name='global.thinking_model_blacklist'
            render={({ field }) => (
              <FormItem>
                <FormLabel>
                  {t('Models that skip thinking suffix processing')}
                </FormLabel>
                <FormControl>
                  <JsonCodeEditor
                    value={field.value}
                    onChange={(value) => field.onChange(value)}
                    name={field.name}
                    onBlur={field.onBlur}
                    textareaRef={field.ref}
                    placeholder={`${t('Example:')}\n${thinkingBlacklistExample}`}
                    heightClassName='h-32 min-h-32 max-h-32'
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Models listed here skip automatic -thinking / -nothinking suffix handling. Matched names are also exempt from @-modifier parsing and 400 validation. Prefix an entry with re: to match the full model name as a Go regular expression, for example re:.*@sha256:.*'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <Separator />

          <div className='space-y-4'>
            <div className='flex items-center gap-2'>
              <h3 className='text-base font-semibold'>
                {t('ChatCompletions -> Responses Compatibility')}
              </h3>
              <StatusBadge
                label={t('Preview')}
                variant='neutral'
                copyable={false}
              />
            </div>

            <Alert>
              <AlertTitle>{t('Warning')}</AlertTitle>
              <AlertDescription>
                {t(
                  'This feature is experimental. Configuration format and behavior may change.'
                )}
              </AlertDescription>
            </Alert>

            <FormField
              control={form.control}
              name='global.chat_completions_to_responses_policy'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Policy JSON')}</FormLabel>
                  <FormControl>
                    <JsonCodeEditor
                      value={field.value}
                      onChange={(value) => field.onChange(value)}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                      placeholder={`${t('Example (specific channels):')}\n${chatToResponsesPolicyExample}\n\n${t('Example (all channels):')}\n${chatToResponsesPolicyAllChannelsExample}`}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Empty value will be saved as {}.')}
                  </FormDescription>
                  <div className='flex flex-wrap gap-2'>
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      onClick={() =>
                        form.setValue(
                          'global.chat_completions_to_responses_policy',
                          chatToResponsesPolicyExample,
                          { shouldDirty: true }
                        )
                      }
                    >
                      {t('Fill example (specific channels)')}
                    </Button>
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      onClick={() =>
                        form.setValue(
                          'global.chat_completions_to_responses_policy',
                          chatToResponsesPolicyAllChannelsExample,
                          { shouldDirty: true }
                        )
                      }
                    >
                      {t('Fill example (all channels)')}
                    </Button>
                  </div>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <Separator />

          <FormField
            control={form.control}
            name='global.azure_force_background_enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Azure Force Background Mode')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Default for the Force background mode switch of Azure channels: new Azure channels start with it, and channels that never set it follow it. Enabled channels submit every request with background=true and the gateway polls for the result; clients still receive a normal synchronous response.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <Separator />

          <FormField
            control={form.control}
            name='general_setting.ping_interval_enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Keep-alive Ping')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Periodically send ping frames to keep streaming connections active.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name='general_setting.ping_interval_seconds'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Ping Interval (seconds)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={1}
                    disabled={!pingEnabled}
                    className='w-24'
                    value={
                      field.value === undefined || field.value === null
                        ? ''
                        : String(field.value)
                    }
                    onChange={(event) => field.onChange(event.target.value)}
                    onBlur={field.onBlur}
                    name={field.name}
                    ref={field.ref}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Recommended to keep this high to avoid upstream throttling.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
