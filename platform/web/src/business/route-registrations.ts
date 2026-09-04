import type { RouteRegistration } from '@mss-boot-io/admin-web/runtime';

// These projections only connect server-owned menu/API paths to compiled UI
// routes. They do not grant access; the Harness backend authorizer is final.
const routeRegistrations: readonly RouteRegistration[] = [
  {
    path: '/harness/overview',
    serverPaths: ['/harness'],
    menuName: 'harness.overview',
    permission: 'harness:read',
  },
  {
    path: '/harness/enrollments',
    serverPaths: ['/admin/api/harness/v1/enrollments'],
    menuName: 'harness.enrollments',
    permission: 'harness:read',
  },
  {
    path: '/harness/endpoints',
    serverPaths: ['/admin/api/harness/v1/endpoints'],
    menuName: 'harness.endpoints',
    permission: 'harness:read',
  },
  {
    path: '/harness/sessions',
    serverPaths: ['/admin/api/harness/v1/sessions'],
    menuName: 'harness.sessions',
    permission: 'harness:read',
  },
  {
    path: '/harness/delivery',
    serverPaths: ['/admin/api/harness/v1/sessions/:id/delivery'],
    menuName: 'harness.delivery',
    permission: 'harness:read',
  },
];

export default routeRegistrations;
