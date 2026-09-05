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
import { useQuery } from '@tanstack/react-query'
import { ArrowRight, ChevronDown, KeyRound, Search, Settings2, WalletCards } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useForm, type SubmitErrorHandler } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { DateTimePicker } from '@/components/datetime-picker'
import {
  SideDrawerSection,
  SideDrawerSectionHeader,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
  sideDrawerSwitchItemClassName,
} from '@/components/drawer-layout'
import { MultiSelect } from '@/components/multi-select'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
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
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { useStatus } from '@/hooks/use-status'
import { getUserModels, getUserGroups } from '@/lib/api'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import { cn } from '@/lib/utils'

import {
  createApiKey,
  updateApiKey,
  getApiKey,
  getTokenAutoGroups,
	getTokenGroupProfiles,
	getTokenGroupProfileHelp,
} from '../api'
import { ERROR_MESSAGES, SUCCESS_MESSAGES } from '../constants'
import {
  getApiKeyFormSchema,
  type ApiKeyFormValues,
  getApiKeyFormDefaultValues,
  transformFormDataToPayload,
  transformApiKeyToFormDefaults,
} from '../lib'
import type { ApiKey } from '../types'
import { type ApiKeyGroupOption } from './api-key-group-combobox'
import { useApiKeys } from './api-keys-provider'
import { AutoGroupOrderEditor } from './auto-group-order-editor'

type ApiKeyMutateDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: ApiKey
}

export function ApiKeysMutateDrawer({
  open,
  onOpenChange,
  currentRow,
}: ApiKeyMutateDrawerProps) {
  const { t } = useTranslation()
  const isUpdate = !!currentRow
  const currentRowId = currentRow?.id
  const { triggerRefresh } = useApiKeys()
  const { status, loading: statusLoading } = useStatus()
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const [initializedTarget, setInitializedTarget] = useState<string | null>(
    null
  )
  const [modelHelpQuery, setModelHelpQuery] = useState('')
  const [groupPickerOpen, setGroupPickerOpen] = useState(false)
  const [profilePickerOpen, setProfilePickerOpen] = useState(false)
  const defaultUseAutoGroup = status?.default_use_auto_group === true

  // Fetch models
  const { data: modelsData } = useQuery({
    queryKey: ['user-models'],
    queryFn: getUserModels,
    enabled: open,
    staleTime: 0,
  })

  // Fetch groups
  const {
    data: groupsData,
    isFetched: groupsFetched,
    isFetching: groupsFetching,
  } = useQuery({
    queryKey: ['user-groups'],
    queryFn: getUserGroups,
    enabled: open,
    staleTime: 0,
  })

  const { data: groupProfilesData } = useQuery({
    queryKey: ['token-group-profiles'],
    queryFn: getTokenGroupProfiles,
    enabled: open,
    staleTime: 60_000,
  })

  const { data: modelHelpData, isFetching: modelHelpFetching } = useQuery({
    queryKey: ['token-group-profile-help', modelHelpQuery.trim()],
    queryFn: () => getTokenGroupProfileHelp(modelHelpQuery.trim()),
    enabled: open && modelHelpQuery.trim().length >= 2,
    staleTime: 30_000,
  })

  const {
    data: apiKeyData,
    isFetched: apiKeyFetched,
    isFetching: apiKeyFetching,
  } = useQuery({
    queryKey: ['api-key', currentRowId],
    queryFn: () => getApiKey(currentRowId ?? 0),
    enabled: open && isUpdate && currentRowId !== undefined,
    staleTime: 0,
  })

  const {
    data: autoGroupsData,
    isFetched: autoGroupsFetched,
    isFetching: autoGroupsFetching,
  } = useQuery({
    queryKey: ['token-auto-groups'],
    queryFn: getTokenAutoGroups,
    enabled: open,
    staleTime: 0,
  })

  const models = modelsData?.data || []
  const groups = useMemo<ApiKeyGroupOption[]>(
    () => {
      const configured = Object.entries(groupsData?.data || {}).map(
        ([key, info]) => ({
        value: key,
        label: key,
        desc: info.desc || key,
        ratio: info.ratio,
        })
      )
      // Keep the custom multi-group entry available even when the administrator
      // has not configured a global Auto group list. The selected groups are
      // still validated by the backend against the user's usable groups.
      if (!configured.some((group) => group.value === 'auto')) {
        configured.unshift({
          value: 'auto',
          label: t('Auto'),
          desc: t('Choose and order the groups this API key will try.'),
          ratio: '',
        })
      }
      return configured
    },
    [groupsData, t]
  )
  const backendHasAuto = groups.some((g) => g.value === 'auto')
  const availableAutoGroupNames = useMemo(
    () => groups.filter((group) => group.value !== 'auto').map((g) => g.value),
    [groups]
  )
  const globalAutoGroups = useMemo(() => {
    const available = new Set(availableAutoGroupNames)
    return (autoGroupsData?.data?.groups || []).filter((group) =>
      available.has(group)
    )
  }, [autoGroupsData, availableAutoGroupNames])
  const globalAutoGroupOptions = useMemo(() => {
    const groupsByValue = new Map(groups.map((group) => [group.value, group]))
    return globalAutoGroups.flatMap((group) => {
      const option = groupsByValue.get(group)
      return option ? [option] : []
    })
  }, [globalAutoGroups, groups])
  const maxAutoGroups =
    Number.isInteger(autoGroupsData?.data?.max_count) &&
    Number(autoGroupsData?.data?.max_count) > 0
      ? Number(autoGroupsData?.data?.max_count)
      : 5
  const schema = useMemo(
    () => getApiKeyFormSchema(t, maxAutoGroups),
    [t, maxAutoGroups]
  )

  const form = useForm<ApiKeyFormValues>({
    resolver: zodResolver(schema),
    defaultValues: getApiKeyFormDefaultValues(defaultUseAutoGroup),
  })
  const selectedProfileId = form.watch('token_group_profile_id') || 0
  const quickProfiles = useMemo(
    () => (groupProfilesData?.data || []).filter((profile) => profile.enabled),
    [groupProfilesData]
  )

  // Load existing data when updating
  useEffect(() => {
    if (!open) {
      setInitializedTarget(null)
      return
    }
    if (
      !groupsFetched ||
      groupsFetching ||
      !autoGroupsFetched ||
      autoGroupsFetching
    ) {
      return
    }
    if (isUpdate && (!apiKeyFetched || apiKeyFetching)) return
    if (!isUpdate && statusLoading) return

    const target = isUpdate && currentRow ? `update:${currentRow.id}` : 'create'
    if (initializedTarget === target) return
    if (isUpdate && currentRow) {
      if (apiKeyData?.success && apiKeyData.data) {
        form.reset(
          transformApiKeyToFormDefaults(
            apiKeyData.data,
            availableAutoGroupNames,
            maxAutoGroups
          )
        )
        setInitializedTarget(target)
      }
    } else {
      form.reset(
        getApiKeyFormDefaultValues(defaultUseAutoGroup && backendHasAuto)
      )
      setInitializedTarget(target)
    }
  }, [
    open,
    isUpdate,
    currentRow,
    form,
    defaultUseAutoGroup,
    statusLoading,
    backendHasAuto,
    groupsFetched,
    groupsFetching,
    autoGroupsFetched,
    autoGroupsFetching,
    apiKeyData,
    apiKeyFetched,
    apiKeyFetching,
    availableAutoGroupNames,
    maxAutoGroups,
    initializedTarget,
  ])

  const formTarget =
    isUpdate && currentRow ? `update:${currentRow.id}` : 'create'
  const isFormInitialized = initializedTarget === formTarget
  const selectedGroup = form.watch('group')
  const selectedAutoGroups = form.watch('auto_groups') || []
  const selectedGroups = selectedGroup === 'auto' ? selectedAutoGroups : selectedGroup ? [selectedGroup] : []
  const selectableGroups = groups.filter((group) => group.value !== 'auto')
  const setSelectedGroups = (values: string[]) => {
    const next = values.slice(0, maxAutoGroups)
    if (next.length <= 1) {
      form.setValue('group', next[0] || '', { shouldDirty: true, shouldValidate: true })
      form.setValue('auto_groups', [], { shouldDirty: true, shouldValidate: true })
      form.setValue('auto_groups_mode', 'inherit', { shouldDirty: true })
      form.setValue('cross_group_retry', false, { shouldDirty: true })
    } else {
      form.setValue('group', 'auto', { shouldDirty: true, shouldValidate: true })
      form.setValue('auto_groups', next, { shouldDirty: true, shouldValidate: true })
      form.setValue('auto_groups_mode', 'custom', { shouldDirty: true })
      form.setValue('cross_group_retry', true, { shouldDirty: true })
    }
  }

  // Correct group after groups load: if the form value is not in available groups, fall back
  useEffect(() => {
    if (groups.length === 0) return
    const currentGroup = selectedGroup
    if (currentGroup && !groups.some((g) => g.value === currentGroup)) {
      const fallback =
        groups.find((g) => g.value === 'default')?.value ??
        groups[0]?.value ??
        ''
      form.setValue('group', fallback)
      if (currentGroup === 'auto') {
        form.setValue('auto_groups', [])
        form.setValue('auto_groups_mode', 'inherit')
        form.setValue('cross_group_retry', false)
      }
    }
  }, [groups, form, selectedGroup])

  const onSubmit = async (data: ApiKeyFormValues) => {
    setIsSubmitting(true)
    try {
      const basePayload = transformFormDataToPayload(data)

      if (isUpdate && currentRow) {
        const result = await updateApiKey({
          ...basePayload,
          id: currentRow.id,
        })
        if (result.success) {
          toast.success(t(SUCCESS_MESSAGES.API_KEY_UPDATED))
          onOpenChange(false)
          triggerRefresh()
        } else {
          toast.error(result.message || t(ERROR_MESSAGES.UPDATE_FAILED))
        }
      } else {
        // Create mode - handle batch creation
        const count = data.tokenCount || 1
        let successCount = 0

        for (let i = 0; i < count; i++) {
          const result = await createApiKey({
            ...basePayload,
            name:
              i === 0 && data.name
                ? data.name
                : `${data.name || 'default'}-${Math.random().toString(36).slice(2, 8)}`,
          })
          if (result.success) {
            successCount++
          } else {
            toast.error(result.message || t(ERROR_MESSAGES.CREATE_FAILED))
            break
          }
        }

        if (successCount > 0) {
          toast.success(
            t('Successfully created {{count}} API Key(s)', {
              count: successCount,
            })
          )
          onOpenChange(false)
          triggerRefresh()
        }
      }
    } catch {
      toast.error(t(ERROR_MESSAGES.UNEXPECTED))
    } finally {
      setIsSubmitting(false)
    }
  }

  const onInvalid: SubmitErrorHandler<ApiKeyFormValues> = () => {
    toast.error(t('Please fix the highlighted fields before saving'))
  }

  const handleSetExpiry = (months: number, days: number, hours: number) => {
    if (months === 0 && days === 0 && hours === 0) {
      form.setValue('expired_time', undefined)
      return
    }

    const now = new Date()
    now.setMonth(now.getMonth() + months)
    now.setDate(now.getDate() + days)
    now.setHours(now.getHours() + hours)

    form.setValue('expired_time', now)
  }

  const { meta: currencyMeta } = getCurrencyDisplay()
  const currencyLabel = getCurrencyLabel()
  const tokensOnly = currencyMeta.kind === 'tokens'
  const quotaLabel = t('Quota ({{currency}})', { currency: currencyLabel })
  const quotaPlaceholder = tokensOnly
    ? t('Enter quota in tokens')
    : t('Enter quota in {{currency}}', { currency: currencyLabel })
  const unlimitedQuota = form.watch('unlimited_quota')
  const modelHelpProfiles = modelHelpData?.data?.profiles?.length
    ? modelHelpData.data.profiles
    : modelHelpQuery.trim().length >= 2
      ? quickProfiles
      : []
  const visibleModelHelpProfiles = modelHelpProfiles.filter(
    (profile) => profile.id !== selectedProfileId
  )
  useEffect(() => {
    if (modelHelpQuery.trim().length >= 2) {
      setProfilePickerOpen(false)
      setGroupPickerOpen((modelHelpData?.data?.groups?.length || 0) <= 5)
    }
  }, [modelHelpQuery, modelHelpData])

  return (
    <Sheet
      open={open}
      onOpenChange={(v) => {
        onOpenChange(v)
        if (!v) {
          form.reset()
        }
      }}
    >
      <SheetContent
        className={sideDrawerContentClassName('max-w-none sm:!max-w-[620px]')}
      >
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {isUpdate ? t('Update API Key') : t('Create API Key')}
          </SheetTitle>
          <SheetDescription>
            {isUpdate
              ? t('Update the API key by providing necessary info.')
              : t('Add a new API key by providing necessary info.')}
          </SheetDescription>
        </SheetHeader>
        <Form {...form}>
          <form
            id='api-key-form'
            onSubmit={form.handleSubmit(onSubmit, onInvalid)}
            aria-busy={!isFormInitialized}
            inert={!isFormInitialized || isSubmitting ? true : undefined}
            className={sideDrawerFormClassName('gap-5')}
          >
            <SideDrawerSection>
              <SideDrawerSectionHeader
                title={t('Basic Information')}
                description={t('Set API key basic information')}
                icon={<KeyRound className='size-4' />}
                iconTone='info'
              />
              <FormField
				control={form.control}
				name='token_group_profile_id'
				render={({ field }) => (
				  <FormItem>
					<FormLabel>{t('Token group')}</FormLabel>
					<FormControl>
					  <NativeSelect value={String(field.value)} onChange={(event) => {
						const profileId = Number(event.target.value)
						field.onChange(profileId)
						if (profileId > 0) {
						  form.setValue('group', 'auto', { shouldDirty: true })
						  form.setValue('auto_groups', [], { shouldDirty: true })
						  form.setValue('auto_groups_mode', 'inherit', { shouldDirty: true })
						  form.setValue('cross_group_retry', true, { shouldDirty: true })
						} else {
						  const fallback = selectableGroups.find((group) => group.value === 'default')?.value || selectableGroups[0]?.value || ''
						  form.setValue('group', fallback, { shouldDirty: true })
						  form.setValue('auto_groups', [], { shouldDirty: true })
						  form.setValue('auto_groups_mode', 'inherit', { shouldDirty: true })
						  form.setValue('cross_group_retry', false, { shouldDirty: true })
						}
					  }}>
						<NativeSelectOption value='0'>{t('Custom')}</NativeSelectOption>
						{(groupProfilesData?.data || []).map((profile) => (
						  <NativeSelectOption key={profile.id} value={String(profile.id)}>
							{profile.name}{profile.recommended ? ` (${t('Recommended')})` : ''}
						  </NativeSelectOption>
						))}
					  </NativeSelect>
					</FormControl>
					<FormDescription>{(groupProfilesData?.data || []).find((profile) => profile.id === field.value)?.description}</FormDescription>
					{selectedProfileId !== 0 && (() => {
					  const profile = quickProfiles.find((item) => item.id === selectedProfileId)
					  if (!profile) return null
					  return (
						<div className='rounded-lg border border-primary/30 bg-primary/5 px-3 py-2.5'>
						  <div className='flex items-center justify-between gap-2'>
							<span className='text-xs font-semibold text-primary'>{profile.name}</span>
							<span className='text-[11px] text-muted-foreground'>{t('Configured route order')}</span>
						  </div>
						  {profile.description && <p className='mt-1 text-xs text-muted-foreground'>{profile.description}</p>}
						  <div className='mt-2 space-y-1.5'>
							{profile.route_groups.map((group, index) => (
								<span key={`${group}-${index}`} className='flex items-start gap-2 rounded-md bg-background px-2 py-1 text-xs font-medium shadow-sm'>
								<span className='shrink-0 text-muted-foreground'>{index + 1}.</span>
								<span className='min-w-0'>
									<span className='block'>{group} ×{String(groups.find((item) => item.value === group)?.ratio ?? '—')}</span>
									<span className='block text-[11px] font-normal leading-relaxed text-muted-foreground'>
										· {groups.find((item) => item.value === group)?.desc || t('Configured group')}
									</span>
								</span>
								</span>
							))}
						  </div>
						</div>
					  )
					})()}
					<FormMessage />
					{quickProfiles.length > 0 && selectedProfileId === 0 && (
					  <Collapsible open={profilePickerOpen} onOpenChange={setProfilePickerOpen} className='rounded-lg border border-primary/20 bg-primary/5 p-2.5'>
					    <CollapsibleTrigger render={<Button type='button' variant='ghost' size='sm' className='h-8 w-full justify-between px-1 text-xs text-primary' />}>
					      <span>{t('Choose portable group')}</span>
					      <ChevronDown className={cn('size-4 transition-transform', profilePickerOpen && 'rotate-180')} />
					    </CollapsibleTrigger>
					    <CollapsibleContent className='pt-2'>
					      <div className='flex flex-wrap gap-2'>
					        {quickProfiles.map((profile) => (
					          <Button key={profile.id} type='button' size='sm' variant='outline' className='h-auto max-w-full gap-1.5 rounded-md px-3 py-1.5 text-left text-xs' onClick={() => {
					            field.onChange(profile.id)
					            form.setValue('group', 'auto', { shouldDirty: true })
					            form.setValue('auto_groups', [], { shouldDirty: true })
					            form.setValue('auto_groups_mode', 'inherit', { shouldDirty: true })
					            form.setValue('cross_group_retry', true, { shouldDirty: true })
					          }}>
					            <span className='min-w-0'>
					              <span className='block truncate font-medium'>{profile.name}</span>
					              {profile.description && <span className='block max-w-56 truncate text-[11px] text-muted-foreground'>{profile.description}</span>}
					            </span>
					          </Button>
					        ))}
					      </div>
					    </CollapsibleContent>
					  </Collapsible>
					)}
				  </FormItem>
				)}
			  />

			  <FormField
                control={form.control}
                name='name'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Name')}</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder={t('Enter a name')} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {selectedProfileId === 0 && <FormField
                control={form.control}
                name='group'
                render={() => (
                  <FormItem>
                    <FormLabel>{t('Group')}</FormLabel>
                    <FormControl>
                      <MultiSelect
                        options={selectableGroups.map((group) => ({
                          label: `${group.label} ×${String(group.ratio ?? '—')}`,
                          value: group.value,
                          description: group.desc,
                        }))}
                        selected={selectedGroups}
                        onChange={setSelectedGroups}
                        placeholder={t('Select groups')}
                        maxVisibleChips={6}
                      />
                    </FormControl>
                    <FormDescription>{t('Select one or more groups. Requests follow this order and use the configured group limit.')}</FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />}

              {selectedProfileId === 0 && <div className='rounded-lg border border-dashed border-muted-foreground/30 p-3'>
                <div className='flex items-center gap-2 text-sm font-medium'>
                  <Search className='size-4 text-muted-foreground' />
                  {t('Find a group by model')}
                </div>
                <p className='mt-1 text-xs text-muted-foreground'>
                  {t('Enter a model name to see available groups and rates.')}
                </p>
                <Input
                  className='mt-2'
                  value={modelHelpQuery}
                  onChange={(event) => setModelHelpQuery(event.target.value)}
                  placeholder={t('For example, opus-4.6 or gpt-5.5')}
                />
                {modelHelpQuery.trim().length >= 2 && (
                  <div className='mt-3 space-y-2'>
                    {modelHelpFetching && <p className='text-xs text-muted-foreground'>{t('Searching...')}</p>}
                    {!modelHelpFetching && modelHelpProfiles.length === 0 && (
                      <p className='text-xs text-muted-foreground'>{t('No matching groups found')}</p>
                    )}
                    {!modelHelpFetching && modelHelpProfiles.length > 0 && (modelHelpData?.data?.exact_match === false || !modelHelpData?.data) && (
                      <p className='text-xs text-muted-foreground'>{t('No exact model match; showing configured groups')}</p>
                    )}
                    {visibleModelHelpProfiles.length > 0 && (
                      <Collapsible open={profilePickerOpen} onOpenChange={setProfilePickerOpen} className='rounded-lg border border-primary/20 bg-primary/5 p-2.5'>
                        <CollapsibleTrigger render={<Button type='button' variant='ghost' size='sm' className='h-8 w-full justify-between px-1 text-xs text-primary' />}>
                          <span>{t('Choose portable group')} · {t('{{count}} available', { count: visibleModelHelpProfiles.length })}</span>
                          <ChevronDown className={cn('size-4 transition-transform', profilePickerOpen && 'rotate-180')} />
                        </CollapsibleTrigger>
                        <CollapsibleContent className='pt-2 space-y-2'>
                        {visibleModelHelpProfiles.slice(0, 3).map((profile) => (
                          <button
                            key={profile.id}
                            type='button'
                            className='flex w-full items-start justify-between gap-3 rounded-md border bg-background px-3 py-2 text-left hover:bg-muted/50'
                            onClick={() => {
                              form.setValue('token_group_profile_id', profile.id, { shouldDirty: true })
                              form.setValue('group', 'auto', { shouldDirty: true })
                              form.setValue('auto_groups', [], { shouldDirty: true })
                              form.setValue('auto_groups_mode', 'inherit', { shouldDirty: true })
                              form.setValue('cross_group_retry', true, { shouldDirty: true })
                            }}
                          >
                        <span className='min-w-0'>
                          <span className='block text-xs font-semibold'>{profile.name}{profile.recommended ? ` · ${t('Recommended')}` : ''}</span>
                          <span className='mt-0.5 block text-xs text-muted-foreground'>{profile.description}</span>
                          <span className='mt-1 block text-[11px] text-muted-foreground'>
                            {profile.model_scope?.length
                              ? `${t('Supported models')}: ${profile.model_scope.join(', ')}`
                              : t('Covers models available in the listed groups')}
                          </span>
                          <span className='mt-1 flex flex-wrap gap-1'>
                            {profile.route_groups.map((group) => (
                              <span key={group} className='rounded bg-muted px-1.5 py-0.5 text-[10px]'>
                                {group} ×{String(modelHelpData?.data?.available_groups?.[group] ?? groups.find((item) => item.value === group)?.ratio ?? '—')} · {groups.find((item) => item.value === group)?.desc || t('Configured group')}
                              </span>
                            ))}
                          </span>
                        </span>
                        <span className='shrink-0 text-xs text-primary'>{t('Use this group')}</span>
                          </button>
                        ))}
                        </CollapsibleContent>
                      </Collapsible>
                    )}
                    {selectedProfileId === 0 && (modelHelpData?.data?.groups || []).length > 0 && (
                      <Collapsible open={groupPickerOpen} onOpenChange={setGroupPickerOpen} className='pt-1'>
                        <CollapsibleTrigger
                          render={<Button type='button' variant='outline' size='sm' className='h-8 w-full justify-between text-xs' />}
                        >
                          <span>{t('Choose ordinary groups')} · {t('{{count}} selected', { count: selectedGroups.length })}</span>
                          <ChevronDown className={cn('size-4 transition-transform', groupPickerOpen && 'rotate-180')} />
                        </CollapsibleTrigger>
                        <CollapsibleContent className='mt-2 space-y-1.5'>
                        <div className='text-xs font-medium text-muted-foreground'>{t('Available groups')}</div>
                        {(modelHelpData?.data?.groups || []).map((group) => {
                          const isSelected = selectedGroups.includes(group.name)
                          const atLimit = selectedGroups.length >= maxAutoGroups && !isSelected
                          return (
                            <button
                              key={group.name}
                              type='button'
                              disabled={atLimit}
                              className='flex w-full items-center justify-between gap-3 rounded-md border bg-background px-3 py-2 text-left hover:bg-muted/50 disabled:cursor-not-allowed disabled:opacity-50'
                              onClick={() => setSelectedGroups(isSelected ? selectedGroups.filter((item) => item !== group.name) : [...selectedGroups, group.name])}
                            >
                              <span className='min-w-0'>
                                <span className='block text-xs font-semibold'>{group.name} ×{String(group.ratio)}{group.matched ? '' : ` · ${t('Candidate')}`}</span>
                                <span className='block text-[11px] font-normal leading-relaxed text-muted-foreground'>{group.desc}</span>
                              </span>
                              <span className='shrink-0 text-xs text-primary'>{isSelected ? t('Selected') : t('Add')}</span>
                            </button>
                          )
                        })}
                        </CollapsibleContent>
                      </Collapsible>
                    )}
                  </div>
                )}
              </div>}

	              {selectedGroup === 'auto' && selectedProfileId === 0 && (
                <FormField
                  control={form.control}
                  name='auto_groups'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Group order')}</FormLabel>
                      <FormDescription>{t('Requests try groups from left to right; if one fails, the next group is tried.')}</FormDescription>
                      <div className='flex items-center gap-2 rounded-md border border-primary/20 bg-primary/5 px-3 py-2 text-xs font-medium text-primary'>
                        <span>{t('Request order')}</span>
                        {selectedAutoGroups.map((group, index) => (
                          <span key={group} className='inline-flex min-w-0 items-center gap-1'>
                            {index > 0 && <ArrowRight className='size-3.5 shrink-0' aria-hidden='true' />}
                            <span className='max-w-28 truncate rounded bg-background px-1.5 py-0.5'>{group}</span>
                          </span>
                        ))}
                      </div>
                      <FormControl>
                        <AutoGroupOrderEditor
                          value={field.value}
                          mode='custom'
                          options={groups}
                          globalOptions={globalAutoGroupOptions}
                          maxCount={maxAutoGroups}
                          onChange={(value) => {
                            form.setValue('auto_groups_mode', 'custom', { shouldDirty: true })
                            form.setValue('auto_groups', value.groups.slice(0, maxAutoGroups), { shouldDirty: true, shouldValidate: true })
                          }}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}

              <FormField
                control={form.control}
                name='expired_time'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Expiration Time')}</FormLabel>
                    <div className='grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center'>
                      <FormControl>
                        <DateTimePicker
                          value={field.value}
                          onChange={field.onChange}
                          placeholder={t('Never expires')}
                          className='min-w-0 [&_input[type=time]]:w-24 sm:[&_input[type=time]]:w-32'
                        />
                      </FormControl>
                      <div className='grid grid-cols-4 gap-2 sm:flex'>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          className='px-2 text-xs sm:px-3 sm:text-sm'
                          onClick={() => handleSetExpiry(0, 0, 0)}
                        >
                          {t('Never')}
                        </Button>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          className='px-2 text-xs sm:px-3 sm:text-sm'
                          onClick={() => handleSetExpiry(1, 0, 0)}
                        >
                          {t('1 Month')}
                        </Button>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          className='px-2 text-xs sm:px-3 sm:text-sm'
                          onClick={() => handleSetExpiry(0, 1, 0)}
                        >
                          {t('1 Day')}
                        </Button>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          className='px-2 text-xs sm:px-3 sm:text-sm'
                          onClick={() => handleSetExpiry(0, 0, 1)}
                        >
                          {t('1 Hour')}
                        </Button>
                      </div>
                    </div>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {!isUpdate && (
                <FormField
                  control={form.control}
                  name='tokenCount'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Quantity')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          min='1'
                          placeholder={t('Number of keys to create')}
                          onChange={(e) =>
                            field.onChange(
                              Number.parseInt(e.target.value, 10) || 1
                            )
                          }
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Create multiple API keys at once (random suffix will be added to names)'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}
            </SideDrawerSection>

            <SideDrawerSection>
              <SideDrawerSectionHeader
                title={t('Quota Settings')}
                description={t('Set quota amount and limits')}
                icon={<WalletCards className='size-4' />}
                iconTone='success'
              />
              {!unlimitedQuota && (
                <FormField
                  control={form.control}
                  name='remain_quota_dollars'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{quotaLabel}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          step={tokensOnly ? 1 : 0.01}
                          placeholder={quotaPlaceholder}
                          onChange={(e) =>
                            field.onChange(
                              Number.parseFloat(e.target.value) || 0
                            )
                          }
                        />
                      </FormControl>
                      <FormDescription>
                        {tokensOnly
                          ? t('Enter the quota amount in tokens')
                          : t('Enter the quota amount in {{currency}}', {
                              currency: currencyLabel,
                            })}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}

              <FormField
                control={form.control}
                name='unlimited_quota'
                render={({ field }) => (
                  <FormItem className={sideDrawerSwitchItemClassName()}>
                    <div className='flex flex-col gap-0.5'>
                      <FormLabel className='text-sm'>
                        {t('Unlimited Quota')}
                      </FormLabel>
                      <FormDescription className='text-xs'>
                        {t('Enable unlimited quota for this API key')}
                      </FormDescription>
                    </div>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </FormItem>
                )}
              />
            </SideDrawerSection>

            <Collapsible open={advancedOpen} onOpenChange={setAdvancedOpen}>
              <SideDrawerSection>
                <CollapsibleTrigger
                  render={
                    <button
                      type='button'
                      className='hover:bg-muted/40 flex w-full items-center gap-3 rounded-md py-1.5 text-left transition-colors'
                    />
                  }
                >
                  <SideDrawerSectionHeader
                    className='flex-1'
                    title={t('Advanced Settings')}
                    description={t('Set API key access restrictions')}
                    icon={<Settings2 className='size-4' />}
                  />
                  <ChevronDown
                    className={cn(
                      'text-muted-foreground size-4 shrink-0 transition-transform',
                      advancedOpen && 'rotate-180'
                    )}
                  />
                </CollapsibleTrigger>
                <CollapsibleContent>
                  <div className='flex flex-col gap-4 pt-2'>
                    <FormField
                      control={form.control}
                      name='model_limits'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Model Limits')}</FormLabel>
                          <FormControl>
                            <MultiSelect
                              options={models.map((m) => ({
                                label: m,
                                value: m,
                              }))}
                              selected={field.value}
                              onChange={field.onChange}
                              placeholder={t(
                                'Select models (empty for allow all)'
                              )}
                            />
                          </FormControl>
                          <FormDescription>
                            {t('Limit which models can be used with this key')}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />

                    <FormField
                      control={form.control}
                      name='allow_ips'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>
                            {t('IP Whitelist (supports CIDR)')}
                          </FormLabel>
                          <FormControl>
                            <Textarea
                              {...field}
                              className='min-h-20 resize-none'
                              placeholder={t(
                                'One IP per line (empty for no restriction)'
                              )}
                              rows={3}
                            />
                          </FormControl>
                          <FormDescription>
                            {t(
                              'Do not over-trust this feature. IP may be spoofed. Please use with nginx, CDN and other gateways.'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </div>
                </CollapsibleContent>
              </SideDrawerSection>
            </Collapsible>
          </form>
        </Form>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose
            render={<Button variant='outline' className='w-full sm:w-auto' />}
          >
            {t('Close')}
          </SheetClose>
          <Button
            type='button'
            onClick={form.handleSubmit(onSubmit, onInvalid)}
            disabled={!isFormInitialized || isSubmitting}
            className='w-full sm:w-auto'
          >
            {isSubmitting ? t('Saving...') : t('Save changes')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
