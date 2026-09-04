import type { AdminBusinessRoute } from '@mss-boot-io/admin-web/business';

// Generated AdminModule routes are composed ahead of this handwritten Thin Host
// extension. Backend authorization remains authoritative for every API call.
const businessRoutes: AdminBusinessRoute[] = [
  { path: '/harness', redirect: '/harness/overview' },
  {
    path: '/harness/overview',
    name: 'harness.overview',
    icon: 'RobotOutlined',
    component: '@/business/pages/Overview',
    access: 'canAccessRoute',
    permission: 'harness:read',
  },
  {
    path: '/harness/enrollments',
    name: 'harness.enrollments',
    component: '@/business/pages/Enrollments',
    access: 'canAccessRoute',
    permission: 'harness:read',
    hideInMenu: true,
  },
  {
    path: '/harness/endpoints',
    name: 'harness.endpoints',
    component: '@/business/pages/Endpoints',
    access: 'canAccessRoute',
    permission: 'harness:read',
    hideInMenu: true,
  },
  {
    path: '/harness/sessions',
    name: 'harness.sessions',
    component: '@/business/pages/Sessions',
    access: 'canAccessRoute',
    permission: 'harness:read',
    hideInMenu: true,
  },
  {
    path: '/harness/delivery',
    name: 'harness.delivery',
    component: '@/business/pages/Delivery',
    access: 'canAccessRoute',
    permission: 'harness:read',
    hideInMenu: true,
  },
];

export default businessRoutes;
