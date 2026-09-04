import ReloadOutlined from '@ant-design/icons/ReloadOutlined';
import { useQuery } from '@tanstack/react-query';
import { useIntl } from '@umijs/max';
import { Button, Card, Col, Row, Statistic } from 'antd';
import { harnessAPI } from '@/business/harness/api';
import { HarnessAsyncContent } from '@/business/harness/HarnessAsyncContent';
import { HarnessPage } from '@/business/harness/HarnessPage';
import { isEmptyOverview } from '@/business/harness/state';

export default function HarnessOverviewPage() {
  const intl = useIntl();
  const query = useQuery({
    queryFn: harnessAPI.loadOverview,
    queryKey: ['harness', 'overview'],
  });

  return (
    <HarnessPage
      description={intl.formatMessage({ id: 'harness.overview.description' })}
      extra={
        <Button icon={<ReloadOutlined />} onClick={() => void query.refetch()}>
          {intl.formatMessage({ id: 'harness.actions.refresh' })}
        </Button>
      }
      title={intl.formatMessage({ id: 'harness.overview.title' })}
    >
      <HarnessAsyncContent
        data={query.data}
        empty={isEmptyOverview}
        emptyMessage={intl.formatMessage({ id: 'harness.overview.empty' })}
        error={query.error}
        loading={query.isPending}
        onRetry={() => void query.refetch()}
        render={(overview) => (
          <Row gutter={[16, 16]}>
            {[
              ['pendingEnrollments', overview.pendingEnrollments],
              ['activeEndpoints', overview.activeEndpoints],
              ['activeSessions', overview.activeSessions],
              ['unacknowledgedFrames', overview.unacknowledgedFrames],
              ['conflictFrames', overview.conflictFrames],
            ].map(([key, value]) => (
              <Col key={key} xs={24} sm={12} xl={8}>
                <Card variant="outlined">
                  <Statistic
                    title={intl.formatMessage({
                      id: `harness.overview.${key}`,
                    })}
                    value={value}
                  />
                </Card>
              </Col>
            ))}
          </Row>
        )}
      />
    </HarnessPage>
  );
}
