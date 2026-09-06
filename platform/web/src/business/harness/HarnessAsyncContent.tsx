import {
  getRequestErrorMessage,
  PageEmpty,
  PageError,
  PageForbidden,
  PageLoading,
} from '@mss-boot-io/admin-web/runtime';
import { useIntl } from '@umijs/max';
import type { ReactNode } from 'react';
import { classifyHarnessPageState } from './state';

export function HarnessAsyncContent<T>({
  data,
  empty,
  emptyMessage,
  error,
  loading,
  onRetry,
  render,
}: {
  data?: T;
  empty: (value: T) => boolean;
  emptyMessage: ReactNode;
  error?: unknown;
  loading: boolean;
  onRetry: () => void;
  render: (value: T) => ReactNode;
}) {
  const intl = useIntl();
  const state = classifyHarnessPageState({
    empty: data === undefined ? false : empty(data),
    error,
    loading,
  });

  switch (state) {
    case 'loading':
      return <PageLoading />;
    case 'forbidden':
      return <PageForbidden message={intl.formatMessage({ id: 'harness.states.forbidden' })} />;
    case 'error':
      return <PageError message={getRequestErrorMessage(error)} onRetry={onRetry} />;
    case 'empty':
      return <PageEmpty description={emptyMessage} />;
    case 'ready':
      return data === undefined ? <PageLoading /> : render(data);
  }
}
