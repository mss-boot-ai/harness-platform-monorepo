import enUS from '../locales/en-US';
import zhCN from '../locales/zh-CN';
import routeRegistrations from '../route-registrations';
import businessRoutes from '../routes.config';

const pagePaths = [
  '/harness/overview',
  '/harness/enrollments',
  '/harness/endpoints',
  '/harness/sessions',
  '/harness/delivery',
];

describe('Harness route and locale registration', () => {
  it('registers every M1 page with read permission and a server projection', () => {
    for (const path of pagePaths) {
      expect(businessRoutes).toContainEqual(
        expect.objectContaining({ access: 'canAccessRoute', path, permission: 'harness:read' }),
      );
      expect(routeRegistrations).toContainEqual(
        expect.objectContaining({ path, permission: 'harness:read' }),
      );
    }
    expect(new Set(routeRegistrations.flatMap((route) => route.serverPaths)).size).toBe(
      routeRegistrations.flatMap((route) => route.serverPaths).length,
    );
  });

  it('keeps Simplified Chinese and English business catalogs in lockstep', () => {
    expect(Object.keys(zhCN).sort()).toEqual(Object.keys(enUS).sort());
    for (const path of pagePaths) {
      const suffix = path.split('/').at(-1);
      expect(zhCN[`menu.harness.${suffix}`]).toBeTruthy();
      expect(enUS[`menu.harness.${suffix}`]).toBeTruthy();
    }
  });
});
