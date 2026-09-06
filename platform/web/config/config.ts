import { defineBusinessAdmin } from '@mss-boot-io/admin-web/business';
import businessRoutes from './business-routes';

export default defineBusinessAdmin({
  businessRoutes,
  routeRegistrations: './src/route-registrations.ts',
  useUtoopack: true,
});
