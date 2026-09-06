import SearchOutlined from '@ant-design/icons/SearchOutlined';
import { useQuery } from '@tanstack/react-query';
import { history, useIntl, useLocation } from '@umijs/max';
import { Button, Card, Descriptions, Input, Space, Table, Typography } from 'antd';
import { useEffect, useState } from 'react';
import { harnessAPI } from '@/business/harness/api';
import type { HarnessAck, HarnessFrame } from '@/business/harness/contract';
import { CompactID, formatHarnessTime, HarnessStatus } from '@/business/harness/format';
import { HarnessAsyncContent } from '@/business/harness/HarnessAsyncContent';
import { HarnessPage } from '@/business/harness/HarnessPage';

function sessionIDFromSearch(search: string): string {
  return new URLSearchParams(search).get('sessionId')?.trim() ?? '';
}

export default function HarnessDeliveryPage() {
  const intl = useIntl();
  const location = useLocation();
  const selectedSessionID = sessionIDFromSearch(location.search);
  const [draftSessionID, setDraftSessionID] = useState(selectedSessionID);
  const query = useQuery({
    enabled: Boolean(selectedSessionID),
    queryFn: () => harnessAPI.loadDelivery(selectedSessionID),
    queryKey: ['harness', 'delivery', selectedSessionID],
  });

  useEffect(() => setDraftSessionID(selectedSessionID), [selectedSessionID]);

  const openSession = () => {
    const sessionID = draftSessionID.trim();
    history.push(
      sessionID
        ? `/harness/delivery?sessionId=${encodeURIComponent(sessionID)}`
        : '/harness/delivery',
    );
  };

  return (
    <HarnessPage
      description={intl.formatMessage({ id: 'harness.delivery.description' })}
      title={intl.formatMessage({ id: 'harness.delivery.title' })}
    >
      <Card className="mb-4" variant="outlined">
        <Space.Compact block>
          <Input
            aria-label={intl.formatMessage({
              id: 'harness.delivery.sessionPlaceholder',
            })}
            placeholder={intl.formatMessage({
              id: 'harness.delivery.sessionPlaceholder',
            })}
            value={draftSessionID}
            onChange={(event) => setDraftSessionID(event.target.value)}
            onPressEnter={openSession}
          />
          <Button icon={<SearchOutlined />} type="primary" onClick={openSession}>
            {intl.formatMessage({ id: 'harness.actions.inspect' })}
          </Button>
        </Space.Compact>
      </Card>

      {!selectedSessionID ? (
        <Typography.Text type="secondary">
          {intl.formatMessage({ id: 'harness.delivery.empty' })}
        </Typography.Text>
      ) : (
        <HarnessAsyncContent
          data={query.data}
          empty={(delivery) => delivery.frames.length === 0 && delivery.acks.length === 0}
          emptyMessage={intl.formatMessage({ id: 'harness.delivery.noFrames' })}
          error={query.error}
          loading={query.isPending}
          onRetry={() => void query.refetch()}
          render={(delivery) => (
            <Space direction="vertical" size="large" style={{ width: '100%' }}>
              <Card
                title={intl.formatMessage({
                  id: 'harness.delivery.sessionSummary',
                })}
              >
                <Descriptions column={{ xs: 1, sm: 2, lg: 3 }} size="small">
                  <Descriptions.Item label={intl.formatMessage({ id: 'harness.fields.session' })}>
                    <CompactID value={delivery.session.id} />
                  </Descriptions.Item>
                  <Descriptions.Item label={intl.formatMessage({ id: 'harness.fields.status' })}>
                    <HarnessStatus value={delivery.session.status} />
                  </Descriptions.Item>
                  <Descriptions.Item
                    label={intl.formatMessage({
                      id: 'harness.fields.keyGeneration',
                    })}
                  >
                    {delivery.session.keyGeneration}
                  </Descriptions.Item>
                  <Descriptions.Item label={intl.formatMessage({ id: 'harness.fields.runtime' })}>
                    {delivery.session.runtimeProfileId}
                  </Descriptions.Item>
                  <Descriptions.Item
                    label={intl.formatMessage({
                      id: 'harness.fields.workspace',
                    })}
                  >
                    {delivery.session.workspaceId}
                  </Descriptions.Item>
                </Descriptions>
              </Card>

              <Card title={intl.formatMessage({ id: 'harness.delivery.frames' })}>
                <Table<HarnessFrame>
                  columns={[
                    {
                      dataIndex: 'messageId',
                      render: (value: string) => <CompactID value={value} />,
                      title: intl.formatMessage({
                        id: 'harness.fields.message',
                      }),
                    },
                    {
                      dataIndex: 'direction',
                      title: intl.formatMessage({
                        id: 'harness.fields.direction',
                      }),
                    },
                    {
                      dataIndex: 'sequence',
                      title: intl.formatMessage({
                        id: 'harness.fields.sequence',
                      }),
                    },
                    {
                      dataIndex: 'keyGeneration',
                      title: intl.formatMessage({
                        id: 'harness.fields.keyGeneration',
                      }),
                    },
                    {
                      dataIndex: 'ciphertextBytes',
                      title: intl.formatMessage({
                        id: 'harness.fields.ciphertextBytes',
                      }),
                    },
                    {
                      dataIndex: 'status',
                      render: (value: string) => <HarnessStatus value={value} />,
                      title: intl.formatMessage({
                        id: 'harness.fields.status',
                      }),
                    },
                    {
                      dataIndex: 'receivedAt',
                      render: (value: string) => formatHarnessTime(value, intl.locale),
                      title: intl.formatMessage({
                        id: 'harness.fields.receivedAt',
                      }),
                    },
                  ]}
                  dataSource={delivery.frames}
                  pagination={false}
                  rowKey="messageId"
                  scroll={{ x: 960 }}
                />
              </Card>

              <Card title={intl.formatMessage({ id: 'harness.delivery.acks' })}>
                <Table<HarnessAck>
                  columns={[
                    {
                      dataIndex: 'direction',
                      title: intl.formatMessage({
                        id: 'harness.fields.direction',
                      }),
                    },
                    {
                      dataIndex: 'senderEndpointId',
                      render: (value: string) => <CompactID value={value} />,
                      title: intl.formatMessage({
                        id: 'harness.fields.sender',
                      }),
                    },
                    {
                      dataIndex: 'receiverEndpointId',
                      render: (value: string) => <CompactID value={value} />,
                      title: intl.formatMessage({
                        id: 'harness.fields.receiver',
                      }),
                    },
                    {
                      dataIndex: 'highestContiguousSequence',
                      title: intl.formatMessage({
                        id: 'harness.fields.highestAck',
                      }),
                    },
                    {
                      dataIndex: 'updatedAt',
                      render: (value: string) => formatHarnessTime(value, intl.locale),
                      title: intl.formatMessage({
                        id: 'harness.fields.updatedAt',
                      }),
                    },
                  ]}
                  dataSource={delivery.acks}
                  pagination={false}
                  rowKey={(ack) =>
                    `${ack.direction}:${ack.senderEndpointId}:${ack.receiverEndpointId}:${ack.keyGeneration}`
                  }
                  scroll={{ x: 960 }}
                />
              </Card>
            </Space>
          )}
        />
      )}
    </HarnessPage>
  );
}
