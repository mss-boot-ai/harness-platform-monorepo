import {
  hasPermission,
  type InitialState,
  PageContainer,
  PageForbidden,
} from '@mss-boot-io/admin-web/runtime';
import { Link, useIntl, useLocation, useModel } from '@umijs/max';
import { Tabs } from 'antd';
import type { ReactNode } from 'react';

export const harnessPermissions = {
  approve: 'harness:approve',
  operate: 'harness:operate',
  read: 'harness:read',
  revoke: 'harness:revoke',
} as const;

const navigation = [
  { path: '/harness/overview', message: 'harness.nav.overview' },
  { path: '/harness/enrollments', message: 'harness.nav.enrollments' },
  { path: '/harness/endpoints', message: 'harness.nav.endpoints' },
  { path: '/harness/sessions', message: 'harness.nav.sessions' },
  { path: '/harness/delivery', message: 'harness.nav.delivery' },
] as const;

export function useHarnessCurrentUser() {
  const { initialState } = useModel('@@initialState') as {
    initialState?: InitialState;
  };
  return initialState?.currentUser;
}

export function HarnessPage({
  children,
  description,
  extra,
  title,
}: {
  children: ReactNode;
  description: ReactNode;
  extra?: ReactNode;
  title: ReactNode;
}) {
  const intl = useIntl();
  const location = useLocation();
  const user = useHarnessCurrentUser();

  if (!hasPermission(user, harnessPermissions.read)) {
    return <PageForbidden message={intl.formatMessage({ id: 'harness.states.forbidden' })} />;
  }

  const activePath =
    navigation.find((item) => location.pathname.startsWith(item.path))?.path ?? '/harness/overview';

  return (
    <PageContainer content={description} extra={extra} title={title}>
      <Tabs
        activeKey={activePath}
        items={navigation.map((item) => ({
          key: item.path,
          label: <Link to={item.path}>{intl.formatMessage({ id: item.message })}</Link>,
        }))}
      />
      {children}
    </PageContainer>
  );
}
