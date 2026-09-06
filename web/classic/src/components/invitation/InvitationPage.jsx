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

import React, { useState, useEffect, useContext } from 'react';
import {
  Card,
  Table,
  Badge,
  Typography,
  Toast,
  Empty,
  Tooltip,
  Button,
  Input,
  Avatar,
  InputNumber,
  Modal,
  Tabs,
  TabPane,
} from '@douyinfe/semi-ui';
import {
  IllustrationNoResult,
  IllustrationNoResultDark,
} from '@douyinfe/semi-illustrations';
import {
  Users,
  Gift,
  Copy,
  TrendingUp,
  Wallet,
  ArrowRightLeft,
  BarChart2,
  Coins,
  History,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { API, showSuccess, showError, timestamp2string } from '../../helpers';
import { renderQuota, renderQuotaWithAmount } from '../../helpers/render';
import { UserContext } from '../../context/User';
import { copy } from '../../helpers/utils';

const { Text } = Typography;

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

const InvitationPage = () => {
  const { t } = useTranslation();
  const [userState, userDispatch] = useContext(UserContext);

  const [loading, setLoading] = useState(false);
  const [users, setUsers] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const [affLink, setAffLink] = useState('');

  // Transfer states
  const [transferAmount, setTransferAmount] = useState('');
  const [transferLoading, setTransferLoading] = useState(false);

  // Transfer logs states
  const [transferLogs, setTransferLogs] = useState([]);
  const [transferLogsTotal, setTransferLogsTotal] = useState(0);
  const [transferLogsPage, setTransferLogsPage] = useState(1);
  const [transferLogsLoading, setTransferLogsLoading] = useState(false);

  useEffect(() => {
    const loadAffLink = async () => {
      try {
        const res = await API.get('/api/user/aff');
        const { success, data } = res.data;
        if (success) {
          setAffLink(`${window.location.origin}/register?aff=${data}`);
        }
      } catch (e) {
        console.error('Load aff link error:', e);
      }
    };
    loadAffLink();
    // 刷新用户数据确保 aff_quota 是最新的
    const refreshUser = async () => {
      try {
        const res = await API.get('/api/user/self');
        if (res.data.success) {
          userDispatch({ type: 'login', payload: res.data.data });
        }
      } catch (e) {
        console.error('Refresh user error:', e);
      }
    };
    refreshUser();
  }, []);

  const loadInvitedUsers = async (currentPage, currentPageSize) => {
    setLoading(true);
    try {
      const res = await API.get(
        `/api/user/self/invited_users?p=${currentPage}&page_size=${currentPageSize}`,
      );
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

  const loadTransferLogs = async (currentPage) => {
    setTransferLogsLoading(true);
    try {
      const res = await API.get(
        `/api/user/self/aff_transfer_logs?p=${currentPage}&page_size=10&_t=${Date.now()}`,
      );
      const { success, data } = res.data;
      if (success) {
        setTransferLogs(data.items || []);
        setTransferLogsTotal(data.total || 0);
      }
    } catch (error) {
      console.error('Load transfer logs error:', error);
    } finally {
      setTransferLogsLoading(false);
    }
  };

  useEffect(() => {
    loadInvitedUsers(page, pageSize);
  }, [page, pageSize]);

  useEffect(() => {
    loadTransferLogs(transferLogsPage);
  }, [transferLogsPage]);

  // 获取 quotaPerUnit 用于金额换算
  const getQuotaPerUnit = () => {
    const qpu = parseFloat(localStorage.getItem('quota_per_unit'));
    return qpu > 0 ? qpu : 500000;
  };

  const handleCopyLink = async () => {
    if (await copy(affLink)) {
      showSuccess(t('邀请链接已复制'));
    }
  };

  const handleTransfer = async () => {
    if (!transferAmount || transferAmount <= 0) {
      showError(t('请输入有效的划转金额'));
      return;
    }
    // 将输入的金额转换为 quota
    const quotaPerUnit = getQuotaPerUnit();
    const quotaToTransfer = Math.round(transferAmount * quotaPerUnit);
    const affQuota = userState?.user?.aff_quota || 0;
    if (quotaToTransfer > affQuota) {
      showError(t('划转金额不能超过待使用收益'));
      return;
    }

    Modal.confirm({
      title: t('确认划转'),
      content: t('确定要将邀请收益划转到钱包余额吗？此操作不可撤销。'),
      okText: t('确认划转'),
      cancelText: t('取消'),
      onOk: async () => {
        setTransferLoading(true);
        try {
          const res = await API.post('/api/user/aff_transfer', {
            quota: quotaToTransfer,
          });
          const { success, message } = res.data;
          if (success) {
            showSuccess(t('划转成功'));
            setTransferAmount('');
            // Refresh user data
            const selfRes = await API.get('/api/user/self');
            if (selfRes.data.success) {
              userDispatch({ type: 'login', payload: selfRes.data.data });
            }
            // Refresh transfer logs
            loadTransferLogs(1);
            setTransferLogsPage(1);
          } else {
            showError(message || t('划转失败'));
          }
        } catch (error) {
          showError(error.response?.data?.message || t('划转失败'));
        } finally {
          setTransferLoading(false);
        }
      },
    });
  };

  const handleTransferAll = () => {
    const affQuota = userState?.user?.aff_quota || 0;
    if (affQuota > 0) {
      const quotaPerUnit = getQuotaPerUnit();
      const moneyAmount = parseFloat((affQuota / quotaPerUnit).toFixed(2));
      setTransferAmount(moneyAmount);
    }
  };

  const invitedColumns = [
    {
      title: t('用户名'),
      dataIndex: 'username',
      key: 'username',
      render: (text, record) => (
        <div>
          <Text strong style={{ color: 'var(--semi-color-text-0)' }}>
            {record.display_name || text}
          </Text>
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
        <Tooltip content={relativeTime(ts, t)}>
          <Text>{timestamp2string(ts)}</Text>
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
          {renderQuotaWithAmount(amount || 0)}
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

  const transferLogColumns = [
    {
      title: t('划转金额'),
      dataIndex: 'quota',
      key: 'quota',
      render: (quota) => (
        <Text strong style={{ color: 'var(--semi-color-success)' }}>
          {renderQuota(quota)}
        </Text>
      ),
    },
    {
      title: t('划转时间'),
      dataIndex: 'created_at',
      key: 'created_at',
      render: (ts) => (
        <Tooltip content={relativeTime(ts, t)}>
          <Text>{timestamp2string(ts)}</Text>
        </Tooltip>
      ),
    },
  ];

  return (
    <div className='space-y-4'>
      {/* 顶部统计区域 - 左右两半 */}
      <div className='grid grid-cols-1 lg:grid-cols-2 gap-4'>
        {/* 左半：邀请奖励 */}
        <Card className='!rounded-2xl shadow-sm border-0'>
          <div className='flex items-center mb-4'>
            <Avatar size='small' color='green' className='mr-3 shadow-md'>
              <Gift size={16} />
            </Avatar>
            <div>
              <Typography.Text className='text-lg font-medium'>
                {t('邀请奖励')}
              </Typography.Text>
              <div
                className='text-xs'
                style={{ color: 'var(--semi-color-text-2)' }}
              >
                {t('邀请好友获得额外奖励')}
              </div>
            </div>
          </div>

          <Card
            className='!rounded-xl'
            style={{ background: 'var(--semi-color-fill-0)' }}
          >
            <div className='grid grid-cols-3 gap-4'>
              <div className='text-center'>
                <div
                  className='text-xl font-bold mb-1'
                  style={{ color: 'var(--semi-color-primary)' }}
                >
                  {renderQuota(userState?.user?.aff_quota || 0)}
                </div>
                <div
                  className='flex items-center justify-center text-xs'
                  style={{ color: 'var(--semi-color-text-2)' }}
                >
                  <Wallet size={12} className='mr-1' />
                  {t('待使用收益')}
                </div>
              </div>
              <div className='text-center'>
                <div
                  className='text-xl font-bold mb-1'
                  style={{ color: 'var(--semi-color-success)' }}
                >
                  {renderQuota(userState?.user?.aff_history_quota || 0)}
                </div>
                <div
                  className='flex items-center justify-center text-xs'
                  style={{ color: 'var(--semi-color-text-2)' }}
                >
                  <TrendingUp size={12} className='mr-1' />
                  {t('累计收益')}
                </div>
              </div>
              <div className='text-center'>
                <div
                  className='text-xl font-bold mb-1'
                  style={{ color: 'var(--semi-color-text-0)' }}
                >
                  {total || 0}
                </div>
                <div
                  className='flex items-center justify-center text-xs'
                  style={{ color: 'var(--semi-color-text-2)' }}
                >
                  <Users size={12} className='mr-1' />
                  {t('邀请人数')}
                </div>
              </div>
            </div>
          </Card>

          {affLink && (
            <div className='mt-4'>
              <Text
                type='secondary'
                size='small'
                className='mb-2 block'
              >
                {t('邀请链接')}
              </Text>
              <Input
                value={affLink}
                addonAfter={
                  <Button
                    icon={<Copy size={14} />}
                    theme='borderless'
                    onClick={handleCopyLink}
                  >
                    {t('复制')}
                  </Button>
                }
                readOnly
                className='!rounded-lg'
              />
            </div>
          )}
        </Card>

        {/* 右半：账户统计 */}
        <Card className='!rounded-2xl shadow-sm border-0'>
          <div className='flex items-center mb-4'>
            <Avatar size='small' color='blue' className='mr-3 shadow-md'>
              <BarChart2 size={16} />
            </Avatar>
            <div>
              <Typography.Text className='text-lg font-medium'>
                {t('账户统计')}
              </Typography.Text>
              <div
                className='text-xs'
                style={{ color: 'var(--semi-color-text-2)' }}
              >
                {t('当前账户的额度和使用概览')}
              </div>
            </div>
          </div>

          <Card
            className='!rounded-xl'
            style={{ background: 'var(--semi-color-fill-0)' }}
          >
            <div className='grid grid-cols-3 gap-4'>
              <div className='text-center'>
                <div
                  className='text-xl font-bold mb-1'
                  style={{ color: 'var(--semi-color-primary)' }}
                >
                  {renderQuota(userState?.user?.quota || 0)}
                </div>
                <div
                  className='flex items-center justify-center text-xs'
                  style={{ color: 'var(--semi-color-text-2)' }}
                >
                  <Wallet size={12} className='mr-1' />
                  {t('当前余额')}
                </div>
              </div>
              <div className='text-center'>
                <div
                  className='text-xl font-bold mb-1'
                  style={{ color: 'var(--semi-color-warning)' }}
                >
                  {renderQuota(userState?.user?.used_quota || 0)}
                </div>
                <div
                  className='flex items-center justify-center text-xs'
                  style={{ color: 'var(--semi-color-text-2)' }}
                >
                  <TrendingUp size={12} className='mr-1' />
                  {t('历史消耗')}
                </div>
              </div>
              <div className='text-center'>
                <div
                  className='text-xl font-bold mb-1'
                  style={{ color: 'var(--semi-color-text-0)' }}
                >
                  {userState?.user?.request_count || 0}
                </div>
                <div
                  className='flex items-center justify-center text-xs'
                  style={{ color: 'var(--semi-color-text-2)' }}
                >
                  <BarChart2 size={12} className='mr-1' />
                  {t('请求次数')}
                </div>
              </div>
            </div>
          </Card>

          {/* 划转余额区域 */}
          <div className='mt-4'>
            <Text
              type='secondary'
              size='small'
              className='mb-2 block'
            >
              {t('划转余额')}
              <span
                className='ml-2'
                style={{ color: 'var(--semi-color-text-3)' }}
              >
                {t('将邀请收益划转到钱包余额')}
              </span>
            </Text>
            <div className='flex gap-2'>
              <InputNumber
                value={transferAmount}
                onChange={(val) => setTransferAmount(val)}
                placeholder={t('输入划转金额')}
                min={0}
                step={0.01}
                precision={2}
                className='flex-1 !rounded-lg'
                prefix='¥'
              />
              <Button
                theme='light'
                type='tertiary'
                onClick={handleTransferAll}
                className='!rounded-lg'
              >
                {t('全部')}
              </Button>
              <Button
                type='primary'
                theme='solid'
                icon={<ArrowRightLeft size={14} />}
                onClick={handleTransfer}
                loading={transferLoading}
                disabled={!transferAmount || transferAmount <= 0}
                className='!rounded-lg'
              >
                {t('划转')}
              </Button>
            </div>
          </div>
        </Card>
      </div>

      {/* 下方 Tabs 区域 */}
      <Card className='!rounded-2xl shadow-sm border-0'>
        <Tabs type='card' defaultActiveKey='invited'>
          {/* 邀请详情 Tab */}
          <TabPane
            tab={
              <div className='flex items-center'>
                <Users size={16} className='mr-2' />
                {t('邀请详情')}
                <Badge
                  count={total}
                  overflowCount={999}
                  style={{ marginLeft: 8 }}
                  type='tertiary'
                />
              </div>
            }
            itemKey='invited'
          >
            <div className='pt-2'>
              <Table
                columns={invitedColumns}
                dataSource={users}
                loading={loading}
                rowKey='id'
                pagination={{
                  currentPage: page,
                  pageSize: pageSize,
                  total: total,
                  showSizeChanger: true,
                  pageSizeOpts: [10, 20, 50],
                  onPageChange: (p) => setPage(p),
                  onPageSizeChange: (ps) => {
                    setPageSize(ps);
                    setPage(1);
                  },
                }}
                size='small'
                empty={
                  <Empty
                    image={
                      <IllustrationNoResult
                        style={{ width: 150, height: 150 }}
                      />
                    }
                    darkModeImage={
                      <IllustrationNoResultDark
                        style={{ width: 150, height: 150 }}
                      />
                    }
                    description={t(
                      '还没有邀请任何人，快分享邀请链接吧！',
                    )}
                    style={{ padding: 30 }}
                  />
                }
              />
            </div>
          </TabPane>

          {/* 划转记录 Tab */}
          <TabPane
            tab={
              <div className='flex items-center'>
                <History size={16} className='mr-2' />
                {t('划转记录')}
                {transferLogsTotal > 0 && (
                  <Badge
                    count={transferLogsTotal}
                    overflowCount={999}
                    style={{ marginLeft: 8 }}
                    type='tertiary'
                  />
                )}
              </div>
            }
            itemKey='transfer_logs'
          >
            <div className='pt-2'>
              <Table
                columns={transferLogColumns}
                dataSource={transferLogs}
                loading={transferLogsLoading}
                rowKey='id'
                pagination={{
                  currentPage: transferLogsPage,
                  pageSize: 10,
                  total: transferLogsTotal,
                  onPageChange: (p) => setTransferLogsPage(p),
                }}
                size='small'
                empty={
                  <Empty
                    image={
                      <IllustrationNoResult
                        style={{ width: 150, height: 150 }}
                      />
                    }
                    darkModeImage={
                      <IllustrationNoResultDark
                        style={{ width: 150, height: 150 }}
                      />
                    }
                    description={t('暂无划转记录')}
                    style={{ padding: 30 }}
                  />
                }
              />
            </div>
          </TabPane>
        </Tabs>
      </Card>
    </div>
  );
};

export default InvitationPage;
