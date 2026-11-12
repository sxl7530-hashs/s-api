import React, { useState, useEffect, useCallback } from 'react';
import {
  Card,
  Form,
  Button,
  Table,
  Tag,
  Typography,
  Space,
} from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess } from '../../helpers';

const { Title } = Typography;

const STATUS_MAP = {
  1: { text: '待处理', color: 'orange' },
  2: { text: '已开票', color: 'green' },
  3: { text: '已拒绝', color: 'red' },
};

const InvoiceRequestPage = () => {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [items, setItems] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const pageSize = 10;

  const fetchData = useCallback(async () => {
    setLoading(true);
    try {
      const res = await API.get(
        `/api/user/invoice_request?p=${page}&page_size=${pageSize}`,
      );
      if (res.data.success && res.data.data) {
        setItems(res.data.data.items || []);
        setTotal(res.data.data.total);
      }
    } catch (e) {
      showError(e.message);
    } finally {
      setLoading(false);
    }
  }, [page]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  const handleSubmit = async (values) => {
    setSubmitting(true);
    try {
      const res = await API.post('/api/user/invoice_request', values);
      if (res.data.success) {
        showSuccess(t('发票申请已提交'));
        fetchData();
      } else {
        showError(res.data.message);
      }
    } catch (e) {
      showError(e.message);
    } finally {
      setSubmitting(false);
    }
  };

  const columns = [
    {
      title: t('公司名称'),
      dataIndex: 'company_name',
    },
    {
      title: t('金额'),
      dataIndex: 'amount',
    },
    {
      title: t('状态'),
      dataIndex: 'status',
      render: (status) => {
        const s = STATUS_MAP[status] || { text: '未知', color: 'grey' };
        return <Tag color={s.color}>{t(s.text)}</Tag>;
      },
    },
    {
      title: t('管理员备注'),
      dataIndex: 'admin_remark',
      render: (text) => text || '-',
    },
    {
      title: t('提交时间'),
      dataIndex: 'created_at',
      render: (ts) => (ts ? new Date(ts * 1000).toLocaleString() : '-'),
    },
  ];

  return (
    <div style={{ padding: '24px' }}>
      <Card
        title={<Title heading={5}>{t('提交发票申请')}</Title>}
        style={{ marginBottom: 24 }}
      >
        <Form
          onSubmit={handleSubmit}
          labelPosition="top"
          style={{ maxWidth: 600 }}
        >
          <Form.Input
            field="company_name"
            label={t('公司名称')}
            placeholder={t('公司名称')}
            rules={[{ required: true, message: t('请填写公司名称') }]}
          />
          <Form.Input
            field="tax_id"
            label={t('税号')}
            placeholder={t('税号')}
          />
          <Form.Input
            field="amount"
            label={t('开票金额')}
            placeholder={t('开票金额')}
            rules={[{ required: true, message: t('请填写开票金额') }]}
          />
          <Form.Input
            field="email"
            label={t('接收邮箱')}
            placeholder={t('接收发票的邮箱')}
            rules={[{ required: true, message: t('请填写邮箱') }]}
          />
          <Form.TextArea
            field="remark"
            label={t('备注')}
            placeholder={t('选填备注')}
            rows={3}
          />
          <Button
            type="primary"
            htmlType="submit"
            loading={submitting}
            style={{ marginTop: 12 }}
          >
            {t('提交申请')}
          </Button>
        </Form>
      </Card>

      <Card title={<Title heading={5}>{t('我的发票申请')}</Title>}>
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
    </div>
  );
};

export default InvoiceRequestPage;
