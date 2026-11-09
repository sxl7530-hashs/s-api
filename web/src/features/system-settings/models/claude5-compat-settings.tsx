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
import { ChevronDown } from 'lucide-react'
import { useId, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { JsonCodeEditor } from '@/components/json-code-editor'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import { SettingsSwitchField } from '../components/settings-form-layout'
import { Claude5CapabilitySettings } from './claude5-capability-settings'
import { Claude5CompatStatistics } from './claude5-compat-statistics'

type Props = { value: string; onChange: (value: string) => void }

const thinkingRules = [
  ['thinking_binding_recover', 'Recover thinking block bindings'],
  ['thinking_disabled_map', 'Map disabled thinking to between_tools'],
  ['thinking_enabled_map', 'Map enabled thinking to adaptive'],
  ['thinking_display_normalize', 'Normalize adaptive thinking display'],
] as const
const fieldRules = [
  ['drop_temperature', 'Drop deprecated temperature'],
  ['clean_empty_system', 'Clean empty system blocks'],
  ['fix_tool_result_cache_control', 'Fix tool result cache control'],
  ['strip_context_management', 'Strip unsupported context management'],
  ['strip_container_fields', 'Strip unsupported container fields'],
  ['map_tool_choice_auto', 'Map tool choice to auto'],
  ['strip_unsupported_web_search', 'Strip unsupported web search'],
] as const

function CompatibilityGroup(props: { title: string; children: ReactNode }) {
  return (
    <Collapsible className='rounded-lg border px-3 py-2'>
      <CollapsibleTrigger className='flex w-full items-center justify-between gap-3 text-left text-sm font-medium'>
        {props.title}
        <ChevronDown className='size-4 shrink-0' aria-hidden='true' />
      </CollapsibleTrigger>
      <CollapsibleContent className='space-y-3 pt-3'>
        {props.children}
      </CollapsibleContent>
    </Collapsible>
  )
}

export function Claude5CompatSettings(props: Props) {
  const { t } = useTranslation()
  const id = useId()
  const [statisticsOpen, setStatisticsOpen] = useState(false)
  let settings: Record<string, unknown> = {}
  let valid = true
  try {
    const parsed: unknown = JSON.parse(props.value || '{}')
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      valid = false
    } else {
      settings = parsed as Record<string, unknown>
    }
  } catch {
    valid = false
  }
  const update = (key: string, value: unknown) =>
    props.onChange(JSON.stringify({ ...settings, [key]: value }, null, 2))
  const fieldDescriptions: Partial<
    Record<(typeof fieldRules)[number][0], string>
  > = {
    map_tool_choice_auto: t(
      'May stop the model from calling the specified tool.'
    ),
    strip_unsupported_web_search: t(
      'Removes the unsupported web search tool and disables its search capability.'
    ),
  }
  const rules = [...thinkingRules, ...fieldRules]
  const count =
    rules.filter(([key]) => settings[key] === true).length +
    (settings.attribution_mode === 'remove' ||
    settings.attribution_mode === 'forge'
      ? 1
      : 0)
  const budgets = Array.isArray(settings.thinking_budget_effort_table)
    ? settings.thinking_budget_effort_table.filter(
        (entry): entry is { max_budget: number; effort: string } =>
          !!entry &&
          typeof entry === 'object' &&
          typeof entry.max_budget === 'number' &&
          typeof entry.effort === 'string'
      )
    : []
  return (
    <Collapsible className='bg-muted/20 rounded-lg border px-4 py-3'>
      <div className='flex flex-wrap items-center gap-3'>
        <CollapsibleTrigger className='flex flex-1 items-center justify-between gap-3 text-left font-semibold'>
          <span>
            {t('AWS Claude 5 Compatibility')}{' '}
            <span className='text-muted-foreground text-xs'>({count})</span>
          </span>
          <ChevronDown className='size-4 shrink-0' aria-hidden='true' />
        </CollapsibleTrigger>
        <SettingsSwitchField
          controlId={`${id}-enabled`}
          label={t('Enable Claude 5 compatibility')}
          checked={settings.enabled === true}
          disabled={!valid}
          onCheckedChange={(value) => update('enabled', value)}
        />
      </div>
      <CollapsibleContent className='space-y-4 pt-4'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Disabled by default. Rules apply only to explicitly selected Bedrock models and preserve the existing request when disabled.'
          )}
        </p>
        {!valid && (
          <p role='alert' className='text-destructive text-sm'>
            {t('Invalid JSON format')}
          </p>
        )}
        <fieldset disabled={!valid} className='space-y-3'>
          <CompatibilityGroup title={t('Scope and direct passthrough')}>
            {(['target_models', 'target_regions'] as const).map((key) => (
              <div className='space-y-2' key={key}>
                <Label htmlFor={`${id}-${key}`}>
                  {t(
                    key === 'target_models'
                      ? 'Target Bedrock model patterns'
                      : 'Target AWS regions'
                  )}
                </Label>
                <Input
                  id={`${id}-${key}`}
                  value={
                    Array.isArray(settings[key]) ? settings[key].join(',') : ''
                  }
                  onChange={(event) =>
                    update(
                      key,
                      event.target.value
                        ? event.target.value
                            .split(',')
                            .map((item) => item.trim())
                        : []
                    )
                  }
                />
              </div>
            ))}
            <p className='text-muted-foreground text-xs'>
              {t(
                'Separate entries with commas. Empty model list matches nothing; empty region list allows all regions.'
              )}
            </p>
            <SettingsSwitchField
              controlId={`${id}-direct`}
              label={t('Apply to AWS direct passthrough users')}
              description={t(
                'When enabled, direct passthrough requests can be modified by the selected rules.'
              )}
              checked={settings.apply_to_passthrough_users === true}
              onCheckedChange={(value) =>
                update('apply_to_passthrough_users', value)
              }
            />
          </CompatibilityGroup>
          <CompatibilityGroup title={t('Rollout and audit sampling')}>
            {(['control_percent', 'audit_sample_percent'] as const).map(
              (key) => (
                <div className='space-y-2' key={key}>
                  <Label htmlFor={`${id}-${key}`}>
                    {key === 'control_percent'
                      ? t('Unmodified control group (%)')
                      : t('Audit sample (%)')}
                  </Label>
                  <Input
                    id={`${id}-${key}`}
                    type='number'
                    min={0}
                    max={100}
                    value={
                      typeof settings[key] === 'number'
                        ? settings[key]
                        : { control_percent: 0, audit_sample_percent: 100 }[key]
                    }
                    onChange={(event) =>
                      update(
                        key,
                        event.target.value === ''
                          ? null
                          : Number(event.target.value)
                      )
                    }
                  />
                </div>
              )
            )}
            <p className='text-muted-foreground text-xs'>
              {t(
                'Control requests keep the original Bedrock body. Sampling affects diagnostics only; sampled counts are not total traffic or proof of a repair.'
              )}
            </p>
          </CompatibilityGroup>
          <CompatibilityGroup title={t('Verified beta capabilities')}>
            <Claude5CapabilitySettings
              kind='beta'
              value={settings.beta_capabilities}
              onChange={(value) => update('beta_capabilities', value)}
            />
          </CompatibilityGroup>
          <CompatibilityGroup title={t('Fingerprint review results')}>
            <Claude5CapabilitySettings
              kind='fingerprint'
              value={settings.fingerprint_statuses}
              onChange={(value) => update('fingerprint_statuses', value)}
            />
          </CompatibilityGroup>
          <CompatibilityGroup title={t('Version attribution')}>
            <Label htmlFor={`${id}-attribution`}>{t('Attribution mode')}</Label>
            <NativeSelect
              id={`${id}-attribution`}
              value={
                typeof settings.attribution_mode === 'string'
                  ? settings.attribution_mode
                  : 'off'
              }
              onChange={(event) =>
                update('attribution_mode', event.target.value)
              }
            >
              <NativeSelectOption value='off'>{t('Off')}</NativeSelectOption>
              <NativeSelectOption value='remove'>
                {t('Remove attribution')}
              </NativeSelectOption>
              <NativeSelectOption value='forge'>
                {t('Rewrite declared version')}
              </NativeSelectOption>
            </NativeSelect>
            <p className='text-muted-foreground text-xs'>
              {t('Attribution processing requires thinking binding recovery')}
            </p>
            {settings.attribution_mode === 'forge' && (
              <div className='space-y-2'>
                <Label htmlFor={`${id}-version`}>
                  {t('Declared client version')}
                </Label>
                <Input
                  id={`${id}-version`}
                  value={
                    typeof settings.forge_version === 'string'
                      ? settings.forge_version
                      : '2.1.280'
                  }
                  onChange={(event) =>
                    update('forge_version', event.target.value)
                  }
                />
                <p className='text-destructive text-xs'>
                  {t(
                    'Declares a different client version. Best effort only; validate with real upstream requests before enabling.'
                  )}
                </p>
              </div>
            )}
            <Label htmlFor={`${id}-fingerprint`}>
              {t('Fingerprint policy')}
            </Label>
            <NativeSelect
              id={`${id}-fingerprint`}
              value={
                typeof settings.fingerprint_policy === 'string'
                  ? settings.fingerprint_policy
                  : 'preserve'
              }
              onChange={(event) =>
                update('fingerprint_policy', event.target.value)
              }
            >
              <NativeSelectOption value='preserve'>
                {t('Preserve fingerprint')}
              </NativeSelectOption>
              <NativeSelectOption value='remove'>
                {t('Remove fingerprint')}
              </NativeSelectOption>
              <NativeSelectOption value='keep_original'>
                {t('Keep original attribution')}
              </NativeSelectOption>
            </NativeSelect>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Fingerprint behavior is only used by forge mode and must be validated with Bedrock.'
              )}
            </p>
          </CompatibilityGroup>
          <CompatibilityGroup title={t('Thinking compatibility')}>
            {thinkingRules.map(([key, label]) => (
              <SettingsSwitchField
                key={key}
                controlId={`${id}-${key}`}
                label={t(label)}
                description={
                  key === 'thinking_binding_recover'
                    ? t(
                        'Experimental. Keep disabled until the target Bedrock model is verified.'
                      )
                    : undefined
                }
                checked={settings[key] === true}
                onCheckedChange={(value) => update(key, value)}
              />
            ))}
            <SettingsSwitchField
              controlId={`${id}-binding-write`}
              label={t('Write thinking block binding control')}
              description={t(
                'Experimental. Keep disabled until the target Bedrock model is verified.'
              )}
              checked={settings.block_binding_write === true}
              onCheckedChange={(value) => update('block_binding_write', value)}
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'Experimental. Keep disabled until the target Bedrock model is verified.'
              )}
            </p>
            <Label htmlFor={`${id}-capability-version`}>
              {t('Beta capability table version')}
            </Label>
            <Input
              id={`${id}-capability-version`}
              value={
                typeof settings.capability_table_version === 'string'
                  ? settings.capability_table_version
                  : 'v1'
              }
              onChange={(event) =>
                update('capability_table_version', event.target.value)
              }
            />
            <Button
              type='button'
              variant='outline'
              onClick={() =>
                update('capability_table_version', crypto.randomUUID())
              }
            >
              {t('Clear temporary beta denials on save')}
            </Button>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Save settings to invalidate temporary beta denials across instances. Configure verified model and region capabilities in the JSON settings.'
              )}
            </p>
            <Label htmlFor={`${id}-display`}>
              {t('Default thinking display')}
            </Label>
            <NativeSelect
              id={`${id}-display`}
              value={
                typeof settings.display_default === 'string'
                  ? settings.display_default
                  : 'summarized'
              }
              onChange={(event) =>
                update('display_default', event.target.value)
              }
            >
              <NativeSelectOption value='summarized'>
                summarized
              </NativeSelectOption>
              <NativeSelectOption value='omitted'>omitted</NativeSelectOption>
            </NativeSelect>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Budget to effort is not equivalent. Without a matching rule, budget is dropped when converting to adaptive.'
              )}
            </p>
            {budgets.map((entry, index) => (
              <div
                key={entry.max_budget}
                className='flex flex-wrap items-center gap-2'
              >
                <Input
                  type='number'
                  min={1}
                  aria-label={t('Maximum thinking budget')}
                  className='w-40'
                  defaultValue={entry.max_budget}
                  onBlur={(event) =>
                    update(
                      'thinking_budget_effort_table',
                      budgets.map((item, i) =>
                        i === index
                          ? { ...item, max_budget: Number(event.target.value) }
                          : item
                      )
                    )
                  }
                />
                <NativeSelect
                  aria-label={t('Thinking effort')}
                  value={entry.effort}
                  onChange={(event) =>
                    update(
                      'thinking_budget_effort_table',
                      budgets.map((item, i) =>
                        i === index
                          ? { ...item, effort: event.target.value }
                          : item
                      )
                    )
                  }
                >
                  {['low', 'medium', 'high'].map((effort) => (
                    <NativeSelectOption key={effort} value={effort}>
                      {effort}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                <Button
                  type='button'
                  variant='outline'
                  onClick={() =>
                    update(
                      'thinking_budget_effort_table',
                      budgets.filter((_, i) => i !== index)
                    )
                  }
                >
                  {t('Delete')}
                </Button>
              </div>
            ))}
            <Button
              type='button'
              variant='outline'
              disabled={budgets.length >= 20}
              onClick={() =>
                update('thinking_budget_effort_table', [
                  ...budgets,
                  {
                    max_budget: (budgets.at(-1)?.max_budget ?? 0) + 8000,
                    effort: 'medium',
                  },
                ])
              }
            >
              {t('Add budget mapping')}
            </Button>
          </CompatibilityGroup>
          <CompatibilityGroup title={t('Request field compatibility')}>
            {fieldRules.map(([key, label]) => (
              <SettingsSwitchField
                key={key}
                controlId={`${id}-${key}`}
                label={t(label)}
                description={fieldDescriptions[key]}
                checked={settings[key] === true}
                onCheckedChange={(value) => update(key, value)}
              />
            ))}
            <Label htmlFor={`${id}-output-policy`}>
              {t('Output config policy')}
            </Label>
            <NativeSelect
              id={`${id}-output-policy`}
              value={
                typeof settings.output_config_policy === 'string'
                  ? settings.output_config_policy
                  : 'none'
              }
              onChange={(event) =>
                update('output_config_policy', event.target.value)
              }
            >
              <NativeSelectOption value='none'>{t('None')}</NativeSelectOption>
              <NativeSelectOption value='strip_all'>
                {t('Remove all')}
              </NativeSelectOption>
              <NativeSelectOption value='keep_effort_only'>
                {t('Keep effort only')}
              </NativeSelectOption>
            </NativeSelect>
          </CompatibilityGroup>
        </fieldset>
        <Collapsible
          open={statisticsOpen}
          onOpenChange={setStatisticsOpen}
          className='rounded-lg border px-3 py-2'
        >
          <CollapsibleTrigger className='flex w-full items-center justify-between gap-3 text-left text-sm font-medium'>
            {t('Compatibility rule-hit statistics')}
            <ChevronDown className='size-4 shrink-0' aria-hidden='true' />
          </CollapsibleTrigger>
          <CollapsibleContent className='pt-3'>
            {statisticsOpen && <Claude5CompatStatistics />}
          </CollapsibleContent>
        </Collapsible>
        <CompatibilityGroup title={t('Claude 5 compatibility settings (JSON)')}>
          <JsonCodeEditor
            value={props.value}
            onChange={props.onChange}
            ariaLabel={t('Claude 5 compatibility settings (JSON)')}
            heightClassName='h-56 min-h-56 max-h-96'
          />
        </CompatibilityGroup>
      </CollapsibleContent>
    </Collapsible>
  )
}
