import React, { useState, useEffect, useCallback } from 'react';
import {
  Card,
  Table,
  Tag,
  Button,
  Modal,
  TextArea,
  Typography,
  Space,
  ButtonGroup,
} from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess } from '../../helpers';

const { Title, Text } = Typography;

const STATUS_MAP = {
  1: { text: '待处理', color: 'orange' },
  2: { text: '已开票', color: 'green' },
  3: { text: '已拒绝', color: 'red' },
};

const AdminInvoiceList = () => {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [items, setItems] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [statusFilter, setStatusFilter] = useState(0);
  const [modalVisible, setModalVisible] = useState(false);
  const [selectedItem, setSelectedItem] = useState(null);
  const [adminRemark, setAdminRemark] = useState('');
  const [updating, setUpdating] = useState(false);
  const pageSize = 20;

  const fetchData = useCallback(async () => {
    setLoading(true);
    try {
      let url = `/api/user/invoice_request/all?p=${page}&page_size=${pageSize}`;
      if (statusFilter > 0) {
        url += `&status=${statusFilter}`;
      }
      const res = await API.get(url);
      if (res.data.success && res.data.data) {
        setItems(res.data.data.items || []);
        setTotal(res.data.data.total);
      }
    } catch (e) {
      showError(e.message);
    } finally {
      setLoading(false);
    }
  }, [page, statusFilter]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  const handleProcess = (item) => {
    setSelectedItem(item);
    setAdminRemark(item.admin_remark || '');
    setModalVisible(true);
  };

  const handleUpdateStatus = async (status) => {
    if (!selectedItem) return;
    setUpdating(true);
    try {
      const res = await API.put(
        `/api/user/invoice_request/${selectedItem.id}`,
        {
          status,
          admin_remark: adminRemark,
        },
      );
      if (res.data.success) {
        showSuccess(t('更新成功'));
        setModalVisible(false);
        fetchData();
      } else {
        showError(res.data.message);
      }
    } catch (e) {
      showError(e.message);
    } finally {
      setUpdating(false);
    }
  };

  const columns = [
    { title: 'ID', dataIndex: 'id', width: 60 },
    { title: t('用户'), dataIndex: 'username', width: 100 },
    { title: t('公司名称'), dataIndex: 'company_name' },
    {
      title: t('税号'),
      dataIndex: 'tax_id',
      render: (text) => text || '-',
    },
    { title: t('金额'), dataIndex: 'amount', width: 100 },
    { title: t('邮箱'), dataIndex: 'email' },
    {
      title: t('备注'),
      dataIndex: 'remark',
      render: (text) => text || '-',
      ellipsis: true,
    },
    {
      title: t('状态'),
      dataIndex: 'status',
      width: 90,
      render: (status) => {
        const s = STATUS_MAP[status] || { text: '未知', color: 'grey' };
        return <Tag color={s.color}>{t(s.text)}</Tag>;
      },
    },
    {
      title: t('提交时间'),
      dataIndex: 'created_at',
      width: 170,
      render: (ts) => (ts ? new Date(ts * 1000).toLocaleString() : '-'),
    },
    {
      title: t('操作'),
      width: 90,
      render: (_, record) =>
        record.status === 1 ? (
          <Button size="small" onClick={() => handleProcess(record)}>
            {t('处理')}
          </Button>
        ) : null,
    },
  ];

  const filterButtons = [
    { value: 0, label: '全部' },
    { value: 1, label: '待处理' },
    { value: 2, label: '已开票' },
    { value: 3, label: '已拒绝' },
  ];

  return (
    <div style={{ padding: '24px' }}>
      <Card
        title={<Title heading={5}>{t('发票管理')}</Title>}
        headerExtraContent={
          <ButtonGroup>
            {filterButtons.map((f) => (
              <Button
                key={f.value}
                type={statusFilter === f.value ? 'primary' : 'tertiary'}
                size="small"
                onClick={() => {
                  setStatusFilter(f.value);
                  setPage(1);
                }}
              >
                {t(f.label)}
              </Button>
            ))}
          </ButtonGroup>
        }
      >
        <Table
          columns={columns}
          dataSource={items}
          loading={loading}
          pagination={{
            currentPage: page,
            pageSize,
            total,
            onPageChange: setPage,
          }}
          rowKey="id"
          empty={t('暂无发票申请')}
        />
      </Card>

      <Modal
        title={t('处理发票申请')}
        visible={modalVisible}
        onCancel={() => setModalVisible(false)}
        footer={
          <Space>
            <Button onClick={() => setModalVisible(false)}>{t('取消')}</Button>
            <Button
              type="danger"
              loading={updating}
              onClick={() => handleUpdateStatus(3)}
            >
              {t('拒绝')}
            </Button>
            <Button
              type="primary"
              loading={updating}
              onClick={() => handleUpdateStatus(2)}
            >
              {t('确认开票')}
            </Button>
          </Space>
        }
      >
        {selectedItem && (
          <div>
            <div
              style={{
                display: 'grid',
                gridTemplateColumns: '1fr 1fr',
                gap: 12,
                marginBottom: 16,
              }}
            >
              <div>
                <Text type="tertiary">{t('公司名称')}: </Text>
                <Text>{selectedItem.company_name}</Text>
              </div>
              <div>
                <Text type="tertiary">{t('税号')}: </Text>
                <Text>{selectedItem.tax_id || '-'}</Text>
              </div>
              <div>
                <Text type="tertiary">{t('金额')}: </Text>
                <Text>{selectedItem.amount}</Text>
              </div>
              <div>
                <Text type="tertiary">{t('邮箱')}: </Text>
                <Text>{selectedItem.email}</Text>
              </div>
            </div>
            {selectedItem.remark && (
              <div style={{ marginBottom: 16 }}>
                <Text type="tertiary">{t('用户备注')}: </Text>
                <Text>{selectedItem.remark}</Text>
              </div>
            )}
            <div>
              <Text
                type="tertiary"
                style={{ display: 'block', marginBottom: 8 }}
              >
                {t('管理员备注')}
              </Text>
              <TextArea
                value={adminRemark}
                onChange={setAdminRemark}
                placeholder={t('选填管理员备注')}
                rows={3}
              />
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
};

export default AdminInvoiceList;
