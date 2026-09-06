import ReloadOutlined from '@ant-design/icons/ReloadOutlined';
import { getRequestErrorMessage, hasPermission } from '@mss-boot-io/admin-web/runtime';
import { useQuery } from '@tanstack/react-query';
import { useIntl } from '@umijs/max';
import { Button, message, Popconfirm, Space, Table } from 'antd';
import { useState } from 'react';
import { harnessAPI } from '@/business/harness/api';
import type { HarnessEndpoint } from '@/business/harness/contract';
import { CompactID, formatHarnessTime, HarnessStatus } from '@/business/harness/format';
import { HarnessAsyncContent } from '@/business/harness/HarnessAsyncContent';
import {
  HarnessPage,
  harnessPermissions,
  useHarnessCurrentUser,
} from '@/business/harness/HarnessPage';

export default function HarnessEndpointsPage() {
  const intl = useIntl();
  const user = useHarnessCurrentUser();
  const [messageAPI, contextHolder] = message.useMessage();
  const [pendingID, setPendingID] = useState('');
  const query = useQuery({
    queryFn: harnessAPI.listEndpoints,
    queryKey: ['harness', 'endpoints'],
  });
  const canRevoke = hasPermission(user, harnessPermissions.revoke);

  const changeEndpoint = async (
    endpoint: HarnessEndpoint,
    action: 'resume' | 'revoke' | 'suspend',
  ) => {
    setPendingID(endpoint.id);
    try {
      await harnessAPI.changeEndpoint(endpoint.id, action);
      messageAPI.success(intl.formatMessage({ id: 'harness.endpoints.actionSaved' }));
      await query.refetch();
    } catch (error) {
      messageAPI.error(getRequestErrorMessage(error));
    } finally {
      setPendingID('');
    }
  };

  return (
    <HarnessPage
      description={intl.formatMessage({ id: 'harness.endpoints.description' })}
      extra={
        <Button icon={<ReloadOutlined />} onClick={() => void query.refetch()}>
          {intl.formatMessage({ id: 'harness.actions.refresh' })}
        </Button>
      }
      title={intl.formatMessage({ id: 'harness.endpoints.title' })}
    >
      {contextHolder}
      <HarnessAsyncContent
        data={query.data}
        empty={(items) => items.length === 0}
        emptyMessage={intl.formatMessage({ id: 'harness.endpoints.empty' })}
        error={query.error}
        loading={query.isPending}
        onRetry={() => void query.refetch()}
        render={(items) => (
          <Table<HarnessEndpoint>
            columns={[
              {
                dataIndex: 'name',
                title: intl.formatMessage({ id: 'harness.fields.name' }),
              },
              {
                dataIndex: 'type',
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
                dataIndex: 'platformName',
                title: intl.formatMessage({ id: 'harness.fields.platform' }),
              },
              {
                dataIndex: 'softwareVersion',
                title: intl.formatMessage({ id: 'harness.fields.version' }),
              },
              {
                dataIndex: 'lastSeenAt',
                render: (value?: string) => formatHarnessTime(value, intl.locale),
                title: intl.formatMessage({ id: 'harness.fields.lastSeenAt' }),
              },
              {
                key: 'actions',
                render: (_value, endpoint) => {
                  if (!canRevoke || endpoint.status === 'REVOKED') return '—';
                  const loading = pendingID === endpoint.id;
                  return (
                    <Space>
                      {endpoint.status === 'ACTIVE' ? (
                        <Button
                          loading={loading}
                          size="small"
                          onClick={() => void changeEndpoint(endpoint, 'suspend')}
                        >
                          {intl.formatMessage({
                            id: 'harness.actions.suspend',
                          })}
                        </Button>
                      ) : null}
                      {endpoint.status === 'SUSPENDED' ? (
                        <Button
                          loading={loading}
                          size="small"
                          onClick={() => void changeEndpoint(endpoint, 'resume')}
                        >
                          {intl.formatMessage({ id: 'harness.actions.resume' })}
                        </Button>
                      ) : null}
                      <Popconfirm
                        description={intl.formatMessage({
                          id: 'harness.endpoints.revokeConfirm',
                        })}
                        title={intl.formatMessage({
                          id: 'harness.actions.revoke',
                        })}
                        onConfirm={() => changeEndpoint(endpoint, 'revoke')}
                      >
                        <Button danger loading={loading} size="small">
                          {intl.formatMessage({ id: 'harness.actions.revoke' })}
                        </Button>
                      </Popconfirm>
                    </Space>
                  );
                },
                title: intl.formatMessage({ id: 'harness.fields.actions' }),
              },
            ]}
            dataSource={items}
            pagination={false}
            rowKey="id"
            scroll={{ x: 1200 }}
          />
        )}
      />
    </HarnessPage>
  );
}
