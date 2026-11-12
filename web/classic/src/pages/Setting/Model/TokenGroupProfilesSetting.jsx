/*
Copyright (C) 2025 QuantumNous

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

import React, { useEffect, useState } from 'react';
import { Button, Form, List, Modal, Space, Tag } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess } from '../../../helpers';

const emptyProfile = () => ({
  name: '',
  slug: '',
  description: '',
  enabled: true,
  display_order: 0,
  recommended: false,
  route_groups: [],
  model_scope: [],
});

export default function TokenGroupProfilesSetting() {
  const { t } = useTranslation();
  const [profiles, setProfiles] = useState([]);
  const [groups, setGroups] = useState([]);
  const [editing, setEditing] = useState(null);
  const [profileDraft, setProfileDraft] = useState(emptyProfile());
  const [loading, setLoading] = useState(false);

  const loadData = async () => {
    setLoading(true);
    try {
      const [profilesRes, groupsRes] = await Promise.all([
        API.get('/api/token-group-profiles/'),
        API.get('/api/group/'),
      ]);
      if (!profilesRes.data.success) {
        showError(profilesRes.data.message);
        return;
      }
      if (!groupsRes.data.success) {
        showError(groupsRes.data.message);
        return;
      }
      setProfiles(profilesRes.data.data || []);
      setGroups(
        (groupsRes.data.data || []).filter((group) => group !== 'auto'),
      );
    } catch (error) {
      showError(error?.message || t('加载令牌分组方案失败'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadData();
  }, []);

  const saveProfile = async (values) => {
    if (!values.name?.trim()) {
      showError(t('请输入名称'));
      return;
    }
    if (
      !Array.isArray(values.route_groups) ||
      values.route_groups.length === 0
    ) {
      showError(t('请至少选择一个路由分组'));
      return;
    }
    setLoading(true);
    try {
      const res = editing?.id
        ? await API.put(`/api/token-group-profiles/${editing.id}`, values)
        : await API.post('/api/token-group-profiles/', values);
      if (!res.data.success) {
        showError(res.data.message);
        return;
      }
      showSuccess(t('保存成功'));
      setEditing(null);
      await loadData();
    } catch (error) {
      showError(error?.message || t('保存失败，请重试'));
    } finally {
      setLoading(false);
    }
  };

  const deleteProfile = async (profile) => {
    setLoading(true);
    try {
      const res = await API.delete(`/api/token-group-profiles/${profile.id}`);
      if (!res.data.success) {
        showError(res.data.message);
        return;
      }
      showSuccess(t('删除成功'));
      await loadData();
    } catch (error) {
      showError(error?.message || t('删除失败'));
    } finally {
      setLoading(false);
    }
  };

  const openNewProfile = () => {
    const profile = emptyProfile();
    setEditing(profile);
    setProfileDraft(profile);
  };

  const openEditProfile = (profile) => {
    setEditing(profile);
    setProfileDraft(profile);
  };

  return (
    <>
      <section className='mb-6 rounded-lg border border-gray-200 p-4'>
        <div className='mb-4 flex flex-wrap items-start justify-between gap-3'>
          <div>
            <h3 className='m-0 text-lg font-semibold'>{t('令牌分组方案')}</h3>
            <p className='mb-0 mt-1 text-sm text-gray-500'>
              {t(
                '创建可复用的令牌分组方案，用户创建令牌时可直接选择，并按配置顺序进行渠道路由。',
              )}
            </p>
          </div>
          <Button theme='solid' loading={loading} onClick={openNewProfile}>
            {t('新增分组方案')}
          </Button>
        </div>
        <List
          loading={loading}
          dataSource={profiles}
          emptyContent={
            <div className='flex flex-col items-center gap-3 py-6'>
              <span>{t('暂无令牌分组方案')}</span>
              <Button onClick={openNewProfile}>{t('新增分组方案')}</Button>
            </div>
          }
          renderItem={(profile) => (
            <List.Item
              main={
                <div>
                  <Space>
                    <strong>{profile.name}</strong>
                    {profile.recommended && (
                      <Tag color='green'>{t('推荐')}</Tag>
                    )}
                    <Tag color={profile.enabled ? 'blue' : 'grey'}>
                      {profile.enabled ? t('已启用') : t('已禁用')}
                    </Tag>
                  </Space>
                  {profile.description && (
                    <div className='mt-1 text-sm text-gray-500'>
                      {profile.description}
                    </div>
                  )}
                  <div className='mt-1 text-xs text-gray-500'>
                    {t('路由顺序')}:&nbsp;
                    {(profile.route_groups || []).join(' → ') || t('暂无')}
                  </div>
                </div>
              }
              extra={
                <Space>
                  <Button size='small' onClick={() => openEditProfile(profile)}>
                    {t('编辑')}
                  </Button>
                  <Button
                    size='small'
                    type='danger'
                    onClick={() =>
                      Modal.confirm({
                        title: t('确定删除此分组方案吗？'),
                        content: t('此修改将不可逆'),
                        onOk: () => deleteProfile(profile),
                      })
                    }
                  >
                    {t('删除')}
                  </Button>
                </Space>
              }
            />
          )}
        />
      </section>

      <Modal
        visible={editing !== null}
        title={editing?.id ? t('编辑令牌分组方案') : t('新增令牌分组方案')}
        onCancel={() => setEditing(null)}
        footer={null}
        keepDOM
      >
        <Form
          key={editing?.id || 'new'}
          initValues={editing || emptyProfile()}
          onValueChange={setProfileDraft}
        >
          <Form.Input
            field='name'
            label={t('名称')}
            rules={[{ required: true, message: t('请输入名称') }]}
          />
          <Form.Input field='slug' label={t('标识')} />
          <Form.TextArea field='description' label={t('描述')} />
          <Form.Select
            field='route_groups'
            label={t('路由分组（按选择顺序）')}
            optionList={groups.map((group) => ({ label: group, value: group }))}
            multiple
            filter
            rules={[{ required: true, message: t('请至少选择一个路由分组') }]}
            style={{ width: '100%' }}
          />
          <Form.TagInput
            field='model_scope'
            label={t('适用模型')}
            extraText={t('留空表示适用于路由分组支持的全部模型')}
          />
          <Form.InputNumber
            field='display_order'
            label={t('显示顺序')}
            min={0}
          />
          <Space>
            <Form.Switch field='enabled' label={t('启用')} />
            <Form.Switch field='recommended' label={t('推荐')} />
          </Space>
          <div className='mt-5 flex justify-end gap-2 border-t border-gray-100 pt-4'>
            <Button disabled={loading} onClick={() => setEditing(null)}>
              {t('取消')}
            </Button>
            <Button
              theme='solid'
              loading={loading}
              onClick={() => saveProfile(profileDraft)}
            >
              {t('保存')}
            </Button>
          </div>
        </Form>
      </Modal>
    </>
  );
}
