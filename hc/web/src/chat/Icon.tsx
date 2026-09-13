import type { ReactNode } from 'react';
export type IconName = 'plus' | 'menu' | 'close' | 'search' | 'settings' | 'arrow' | 'chevron' | 'copy' | 'check' | 'code' | 'terminal' | 'spark' | 'lock' | 'stop' | 'down' | 'monitor';
const paths: Record<IconName, ReactNode> = {
  plus: <path d="M12 5v14M5 12h14" />,
  menu: <><rect x="3" y="4" width="18" height="16" rx="3" /><path d="M9 4v16" /></>,
  close: <path d="m6 6 12 12M6 18 18 6" />,
  search: <><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 4.5 4.5" /></>,
  settings: <><path d="M4 7h16M4 17h16" /><circle cx="9" cy="7" r="2" /><circle cx="15" cy="17" r="2" /></>,
  arrow: <path d="M12 19V5m-6 6 6-6 6 6" />,
  chevron: <path d="m7 10 5 5 5-5" />,
  copy: <><rect x="8" y="8" width="12" height="13" rx="2" /><path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3" /></>,
  check: <path d="m5 12 4 4L19 6" />,
  code: <path d="m8 6-6 6 6 6m8-12 6 6-6 6m-3-14-2 16" />,
  terminal: <><rect x="3" y="4" width="18" height="16" rx="3" /><path d="m7 9 3 3-3 3m6 0h4" /></>,
  spark: <path d="m12 3 2.7 6.3L21 12l-6.3 2.7L12 21l-2.7-6.3L3 12l6.3-2.7L12 3Z" />,
  lock: <><rect x="5" y="10" width="14" height="11" rx="3" /><path d="M8 10V7a4 4 0 0 1 8 0v3m-4 5v2" /></>,
  stop: <rect x="6" y="6" width="12" height="12" rx="2" />,
  down: <path d="M12 5v14m-6-6 6 6 6-6" />,
  monitor: <><rect x="3" y="3" width="18" height="14" rx="2" /><path d="M12 17v4m-4 0h8" /></>,
};
export function Icon({ name }: { readonly name: IconName }) {
  return <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{paths[name]}</svg>;
}
