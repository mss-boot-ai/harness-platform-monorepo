import ReloadOutlined from '@ant-design/icons/ReloadOutlined';
import { getRequestErrorMessage, hasPermission } from '@mss-boot-io/admin-web/runtime';
import { useQuery } from '@tanstack/react-query';
import { Link, useIntl } from '@umijs/max';
import { Button, message, Popconfirm, Space, Table } from 'antd';
import { useState } from 'react';
import { harnessAPI } from '@/business/harness/api';
import type { HarnessSession } from '@/business/harness/contract';
import { CompactID, formatHarnessTime, HarnessStatus } from '@/business/harness/format';
import { HarnessAsyncContent } from '@/business/harness/HarnessAsyncContent';
import {
  HarnessPage,
  harnessPermissions,
  useHarnessCurrentUser,
} from '@/business/harness/HarnessPage';

const terminalSessionStatuses = new Set(['ABA_REVOKED', 'CLOSED', 'FAILED']);

export default function HarnessSessionsPage() {
  const intl = useIntl();
  const user = useHarnessCurrentUser();
  const [messageAPI, contextHolder] = message.useMessage();
  const [pendingID, setPendingID] = useState('');
  const query = useQuery({
    queryFn: harnessAPI.listSessions,
    queryKey: ['harness', 'sessions'],
  });
  const canOperate = hasPermission(user, harnessPermissions.operate);

  const closeSession = async (session: HarnessSession) => {
    setPendingID(session.id);
    try {
      await harnessAPI.closeSession(session.id);
      messageAPI.success(intl.formatMessage({ id: 'harness.sessions.closed' }));
      await query.refetch();
    } catch (error) {
      messageAPI.error(getRequestErrorMessage(error));
    } finally {
      setPendingID('');
    }
  };

  return (
    <HarnessPage
      description={intl.formatMessage({ id: 'harness.sessions.description' })}
      extra={
        <Button icon={<ReloadOutlined />} onClick={() => void query.refetch()}>
          {intl.formatMessage({ id: 'harness.actions.refresh' })}
        </Button>
      }
      title={intl.formatMessage({ id: 'harness.sessions.title' })}
    >
      {contextHolder}
      <HarnessAsyncContent
        data={query.data}
        empty={(items) => items.length === 0}
        emptyMessage={intl.formatMessage({ id: 'harness.sessions.empty' })}
        error={query.error}
        loading={query.isPending}
        onRetry={() => void query.refetch()}
        render={(items) => (
          <Table<HarnessSession>
            columns={[
              {
                dataIndex: 'id',
                render: (value: string) => <CompactID value={value} />,
                title: intl.formatMessage({ id: 'harness.fields.session' }),
              },
              {
                dataIndex: 'status',
                render: (value: string) => <HarnessStatus value={value} />,
                title: intl.formatMessage({ id: 'harness.fields.status' }),
              },
              {
                dataIndex: 'runtimeProfileId',
                title: intl.formatMessage({ id: 'harness.fields.runtime' }),
              },
              {
                dataIndex: 'workspaceId',
                title: intl.formatMessage({ id: 'harness.fields.workspace' }),
              },
              {
                dataIndex: 'keyGeneration',
                title: intl.formatMessage({
                  id: 'harness.fields.keyGeneration',
                }),
              },
              {
                dataIndex: 'lastActivityAt',
                render: (value?: string) => formatHarnessTime(value, intl.locale),
                title: intl.formatMessage({
                  id: 'harness.fields.lastActivityAt',
                }),
              },
              {
                key: 'actions',
                render: (_value, session) => (
                  <Space>
                    <Link to={`/harness/delivery?sessionId=${encodeURIComponent(session.id)}`}>
                      {intl.formatMessage({ id: 'harness.actions.delivery' })}
                    </Link>
                    {canOperate && !terminalSessionStatuses.has(session.status) ? (
                      <Popconfirm
                        description={intl.formatMessage({
                          id: 'harness.sessions.closeConfirm',
                        })}
                        title={intl.formatMessage({
                          id: 'harness.actions.close',
                        })}
                        onConfirm={() => closeSession(session)}
                      >
                        <Button danger loading={pendingID === session.id} size="small">
                          {intl.formatMessage({ id: 'harness.actions.close' })}
                        </Button>
                      </Popconfirm>
                    ) : null}
                  </Space>
                ),
                title: intl.formatMessage({ id: 'harness.fields.actions' }),
              },
            ]}
            dataSource={items}
            pagination={false}
            rowKey="id"
            scroll={{ x: 1120 }}
          />
        )}
      />
    </HarnessPage>
  );
}
