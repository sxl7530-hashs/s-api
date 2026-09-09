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

import React, { useState, useEffect } from 'react';
import {
  Modal,
  Table,
  Badge,
  Typography,
  Toast,
  Empty,
  Tooltip,
} from '@douyinfe/semi-ui';
import {
  IllustrationNoResult,
  IllustrationNoResultDark,
} from '@douyinfe/semi-illustrations';
import { Users } from 'lucide-react';
import { API, timestamp2string } from '../../../helpers';
import { useIsMobile } from '../../../hooks/common/useIsMobile';

const { Text } = Typography;

// 计算相对时间
function relativeTime(unixSeconds, t) {
  if (!unixSeconds) return '-';
  const now = Math.floor(Date.now() / 1000);
  const diff = now - unixSeconds;
  if (diff < 60) return t('刚刚');
  if (diff < 3600) return `${Math.floor(diff / 60)} ${t('分钟前')}`;
  if (diff < 86400) return `${Math.floor(diff / 3600)} ${t('小时前')}`;
  if (diff < 2592000) return `${Math.floor(diff / 86400)} ${t('天前')}`;
  if (diff < 31536000) return `${Math.floor(diff / 2592000)} ${t('个月前')}`;
  return `${Math.floor(diff / 31536000)} ${t('年前')}`;
}

const InvitedUsersModal = ({ visible, onCancel, t, targetUserId }) => {
  const [loading, setLoading] = useState(false);
  const [users, setUsers] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);

  const isMobile = useIsMobile();

  const loadInvitedUsers = async (currentPage, currentPageSize) => {
    setLoading(true);
    try {
      let url = `/api/user/self/invited_users?p=${currentPage}&page_size=${currentPageSize}`;
      if (targetUserId) {
        url += `&user_id=${targetUserId}`;
      }
      const res = await API.get(url);
      const { success, message, data } = res.data;
      if (success) {
        setUsers(data.items || []);
        setTotal(data.total || 0);
      } else {
        Toast.error({ content: message || t('加载失败') });
      }
    } catch (error) {
      console.error('Load invited users error:', error);
      Toast.error({ content: t('加载失败') });
    } finally {
      setLoading(false);
    }
  };

  // Reset page when modal opens or targetUserId changes
  useEffect(() => {
    if (visible) {
      setPage(1);
      setUsers([]);
      setTotal(0);
    }
  }, [visible, targetUserId]);

  useEffect(() => {
    if (visible) {
      loadInvitedUsers(page, pageSize);
    }
  }, [visible, page, pageSize, targetUserId]);

  const handlePageChange = (currentPage) => {
    setPage(currentPage);
  };

  const handlePageSizeChange = (currentPageSize) => {
    setPageSize(currentPageSize);
    setPage(1);
  };

  const columns = [
    {
      title: t('用户名'),
      dataIndex: 'username',
      key: 'username',
      render: (text, record) => (
        <div>
          <Text strong style={{ color: 'var(--semi-color-text-0)' }}>{record.display_name || text}</Text>
          {record.display_name && record.display_name !== text && (
            <div>
              <Text type='tertiary' size='small'>
                @{text}
              </Text>
            </div>
          )}
        </div>
      ),
    },
    {
      title: t('注册时间'),
      dataIndex: 'created_at',
      key: 'created_at',
      render: (ts) => (
        <Tooltip content={timestamp2string(ts)}>
          <Text>{relativeTime(ts, t)}</Text>
        </Tooltip>
      ),
    },
    {
      title: t('充值次数'),
      dataIndex: 'topup_count',
      key: 'topup_count',
      render: (count) => <Text>{count || 0}</Text>,
    },
    {
      title: t('累计充值'),
      dataIndex: 'total_topup',
      key: 'total_topup',
      render: (amount) => (
        <Text type={amount > 0 ? 'success' : 'tertiary'}>
          ${amount || 0}
        </Text>
      ),
    },
    {
      title: t('状态'),
      dataIndex: 'status',
      key: 'status',
      render: (status) => (
        <span className='flex items-center gap-2'>
          <Badge dot type={status === 1 ? 'success' : 'danger'} />
          <span>{status === 1 ? t('正常') : t('已禁用')}</span>
        </span>
      ),
    },
  ];

  const titleText = targetUserId
    ? `${t('邀请详情')} (ID: ${targetUserId})`
    : t('邀请详情');

  return (
    <Modal
      title={
        <div className='flex items-center'>
          <Users className='mr-2' size={18} />
          {titleText}
        </div>
      }
      visible={visible}
      onCancel={onCancel}
      footer={null}
      size={isMobile ? 'full-width' : 'large'}
    >
      <Table
        columns={columns}
        dataSource={users}
        loading={loading}
        rowKey='id'
        pagination={{
          currentPage: page,
          pageSize: pageSize,
          total: total,
          showSizeChanger: true,
          pageSizeOpts: [10, 20, 50],
          onPageChange: handlePageChange,
          onPageSizeChange: handlePageSizeChange,
        }}
        size='small'
        empty={
          <Empty
            image={
              <IllustrationNoResult style={{ width: 150, height: 150 }} />
            }
            darkModeImage={
              <IllustrationNoResultDark
                style={{ width: 150, height: 150 }}
              />
            }
            description={t('还没有邀请任何人，快分享邀请链接吧！')}
            style={{ padding: 30 }}
          />
        }
      />
    </Modal>
  );
};

export default InvitedUsersModal;
