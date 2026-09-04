import ReloadOutlined from '@ant-design/icons/ReloadOutlined';
import { getRequestErrorMessage, hasPermission } from '@mss-boot-io/admin-web/runtime';
import { useQuery } from '@tanstack/react-query';
import { useIntl } from '@umijs/max';
import { Button, Input, Modal, message, Space, Table } from 'antd';
import { useState } from 'react';
import { harnessAPI } from '@/business/harness/api';
import type { HarnessEnrollment } from '@/business/harness/contract';
import { CompactID, formatHarnessTime, HarnessStatus } from '@/business/harness/format';
import { HarnessAsyncContent } from '@/business/harness/HarnessAsyncContent';
import {
  HarnessPage,
  harnessPermissions,
  useHarnessCurrentUser,
} from '@/business/harness/HarnessPage';

interface EnrollmentDecision {
  decision: 'approve' | 'deny';
  enrollment: HarnessEnrollment;
}

export default function HarnessEnrollmentsPage() {
  const intl = useIntl();
  const user = useHarnessCurrentUser();
  const [messageAPI, contextHolder] = message.useMessage();
  const [decision, setDecision] = useState<EnrollmentDecision>();
  const [userCode, setUserCode] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const query = useQuery({
    queryFn: harnessAPI.listEnrollments,
    queryKey: ['harness', 'enrollments'],
  });
  const canApprove = hasPermission(user, harnessPermissions.approve);

  const closeDecision = () => {
    setDecision(undefined);
    setUserCode('');
  };

  const submitDecision = async () => {
    if (!decision || !userCode.trim()) return;
    setSubmitting(true);
    try {
      await harnessAPI.decideEnrollment(decision.enrollment.id, decision.decision, userCode.trim());
      messageAPI.success(intl.formatMessage({ id: 'harness.enrollments.decisionSaved' }));
      closeDecision();
      await query.refetch();
    } catch (error) {
      messageAPI.error(getRequestErrorMessage(error));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <HarnessPage
      description={intl.formatMessage({
        id: 'harness.enrollments.description',
      })}
      extra={
        <Button icon={<ReloadOutlined />} onClick={() => void query.refetch()}>
          {intl.formatMessage({ id: 'harness.actions.refresh' })}
        </Button>
      }
      title={intl.formatMessage({ id: 'harness.enrollments.title' })}
    >
      {contextHolder}
      <HarnessAsyncContent
        data={query.data}
        empty={(items) => items.length === 0}
        emptyMessage={intl.formatMessage({ id: 'harness.enrollments.empty' })}
        error={query.error}
        loading={query.isPending}
        onRetry={() => void query.refetch()}
        render={(items) => (
          <Table<HarnessEnrollment>
            columns={[
              {
                dataIndex: 'endpointName',
                title: intl.formatMessage({ id: 'harness.fields.endpoint' }),
              },
              {
                dataIndex: 'endpointType',
                title: intl.formatMessage({ id: 'harness.fields.type' }),
              },
              {
                dataIndex: 'status',
                render: (value: string) => <HarnessStatus value={value} />,
                title: intl.formatMessage({ id: 'harness.fields.status' }),
              },
              {
                dataIndex: 'id',
                render: (value: string) => <CompactID value={value} />,
                title: intl.formatMessage({ id: 'harness.fields.id' }),
              },
              {
                dataIndex: 'expiresAt',
                render: (value: string) => formatHarnessTime(value, intl.locale),
                title: intl.formatMessage({ id: 'harness.fields.expiresAt' }),
              },
              {
                key: 'actions',
                render: (_value, enrollment) =>
                  canApprove && enrollment.status === 'PENDING' ? (
                    <Space>
                      <Button
                        size="small"
                        type="primary"
                        onClick={() => setDecision({ decision: 'approve', enrollment })}
                      >
                        {intl.formatMessage({ id: 'harness.actions.approve' })}
                      </Button>
                      <Button
                        danger
                        size="small"
                        onClick={() => setDecision({ decision: 'deny', enrollment })}
                      >
                        {intl.formatMessage({ id: 'harness.actions.deny' })}
                      </Button>
                    </Space>
                  ) : (
                    '—'
                  ),
                title: intl.formatMessage({ id: 'harness.fields.actions' }),
              },
            ]}
            dataSource={items}
            pagination={false}
            rowKey="id"
            scroll={{ x: 960 }}
          />
        )}
      />
      <Modal
        confirmLoading={submitting}
        destroyOnHidden
        okButtonProps={{ disabled: !userCode.trim() }}
        okText={intl.formatMessage({
          id: decision?.decision === 'deny' ? 'harness.actions.deny' : 'harness.actions.approve',
        })}
        open={Boolean(decision)}
        title={intl.formatMessage({ id: 'harness.enrollments.userCodeTitle' })}
        onCancel={closeDecision}
        onOk={() => void submitDecision()}
      >
        <Input
          autoComplete="one-time-code"
          maxLength={64}
          placeholder={intl.formatMessage({
            id: 'harness.enrollments.userCodePlaceholder',
          })}
          value={userCode}
          onChange={(event) => setUserCode(event.target.value)}
        />
      </Modal>
    </HarnessPage>
  );
}
