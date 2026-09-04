import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus, Save, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { MultiSelect, type Option } from '@/components/multi-select'
import { api } from '@/lib/api'

type Profile = { id: number; name: string; slug: string; description: string; enabled: boolean; display_order: number; recommended: boolean; route_groups: string[]; model_scope?: string[] }
type Draft = Omit<Profile, 'id'> & { id?: number }

async function listProfiles() { const res = await api.get<{ data: Profile[] }>('/api/token-group-profiles/'); return res.data.data || [] }
async function listGroups() { const res = await api.get<{ data: string[] }>('/api/group/'); return res.data.data || [] }
async function saveProfile(profile: Draft) { const payload = { ...profile, route_groups: profile.route_groups, model_scope: profile.model_scope }; const res = profile.id ? await api.put(`/api/token-group-profiles/${profile.id}`, payload) : await api.post('/api/token-group-profiles/', payload); return res.data }
async function removeProfile(id: number) { await api.delete(`/api/token-group-profiles/${id}`) }

const emptyDraft = (): Draft => ({ name: '', slug: '', description: '', enabled: true, display_order: 0, recommended: false, route_groups: [], model_scope: [] })

export function TokenGroupProfilesCard() {
  const { t } = useTranslation(); const queryClient = useQueryClient(); const [draft, setDraft] = useState<Draft | null>(null)
  const query = useQuery({ queryKey: ['admin-token-group-profiles'], queryFn: listProfiles, staleTime: 30_000 })
  const groupsQuery = useQuery({ queryKey: ['admin-route-groups'], queryFn: listGroups, staleTime: 60_000 })
  const save = useMutation({ mutationFn: saveProfile, onSuccess: () => { toast.success(t('Saved')); setDraft(null); queryClient.invalidateQueries({ queryKey: ['admin-token-group-profiles'] }); queryClient.invalidateQueries({ queryKey: ['token-group-profiles'] }) } })
  const remove = useMutation({ mutationFn: removeProfile, onSuccess: () => { toast.success(t('Deleted')); queryClient.invalidateQueries({ queryKey: ['admin-token-group-profiles'] }); queryClient.invalidateQueries({ queryKey: ['token-group-profiles'] }) } })
  const groupOptions: Option[] = (groupsQuery.data || []).filter((group) => group !== 'auto').map((group) => ({ value: group, label: group }))
  return <Card>
    <CardHeader className='flex-row items-center justify-between'><CardTitle>{t('Token group profiles')}</CardTitle><Button size='sm' onClick={() => setDraft(emptyDraft())}><Plus className='mr-1 size-4' />{t('Add')}</Button></CardHeader>
    <CardContent className='space-y-3'>
      <p className='text-sm text-muted-foreground'>{t('Create reusable token presets with ordered routing groups.')}</p>
      {query.data?.map((profile) => <div key={profile.id} className='flex items-center justify-between rounded-md border p-3'><div><div className='font-medium'>{profile.name}{profile.recommended ? ` · ${t('Recommended')}` : ''}</div><div className='text-xs text-muted-foreground'>{profile.route_groups.join(' → ') || t('No route groups')} · {profile.enabled ? t('Enabled') : t('Disabled')}</div></div><div className='flex gap-2'><Button variant='outline' size='sm' onClick={() => setDraft(profile)}>{t('Edit')}</Button><Button variant='ghost' size='icon' onClick={() => remove.mutate(profile.id)} aria-label={t('Delete')}><Trash2 className='size-4' /></Button></div></div>)}
      {draft && <div className='space-y-3 rounded-md border p-4'><div className='grid gap-3 sm:grid-cols-2'><div><Label>{t('Name')}</Label><Input value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} /></div><div><Label>{t('Slug')}</Label><Input value={draft.slug} onChange={(e) => setDraft({ ...draft, slug: e.target.value })} /></div></div><div><Label>{t('Description')}</Label><Input value={draft.description} onChange={(e) => setDraft({ ...draft, description: e.target.value })} /></div><div><Label>{t('Route groups (ordered)')}</Label><p className='mb-2 text-xs text-muted-foreground'>{t('Search and select groups. The selected order is used for routing.')}</p><MultiSelect options={groupOptions} selected={draft.route_groups} onChange={(values) => setDraft({ ...draft, route_groups: values })} placeholder={t('Search route groups')} maxVisibleChips={8} /></div><div className='flex flex-wrap gap-6'><label className='flex items-center gap-2 text-sm'><Switch checked={draft.enabled} onCheckedChange={(checked) => setDraft({ ...draft, enabled: checked })} />{t('Enabled')}</label><label className='flex items-center gap-2 text-sm'><Switch checked={draft.recommended} onCheckedChange={(checked) => setDraft({ ...draft, recommended: checked })} />{t('Recommended')}</label></div><div className='flex justify-end gap-2'><Button variant='outline' onClick={() => setDraft(null)}>{t('Cancel')}</Button><Button disabled={save.isPending || !draft.name || draft.route_groups.length === 0} onClick={() => save.mutate({ ...draft, model_scope: [] })}><Save className='mr-1 size-4' />{t('Save')}</Button></div></div>}
    </CardContent>
  </Card>
}
