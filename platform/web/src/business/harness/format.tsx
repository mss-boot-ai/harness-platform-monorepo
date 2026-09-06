import { Tag, Typography } from 'antd';

const statusColors: Record<string, string> = {
  ACTIVE: 'green',
  APPROVED: 'blue',
  CLOSED: 'default',
  CONFLICT: 'red',
  DENIED: 'red',
  FAILED: 'red',
  PENDING: 'gold',
  REKEY_REQUIRED: 'orange',
  REVOKED: 'red',
  SUSPENDED: 'orange',
  UNCERTAIN: 'volcano',
};

export function HarnessStatus({ value }: { value: string }) {
  return <Tag color={statusColors[value] ?? 'default'}>{value}</Tag>;
}

export function CompactID({ value }: { value: string }) {
  return (
    <Typography.Text copyable={{ text: value }} ellipsis style={{ maxWidth: 180 }}>
      {value}
    </Typography.Text>
  );
}

export function formatHarnessTime(value: string | undefined, locale: string): string {
  if (!value) return '—';
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return value;
  return new Intl.DateTimeFormat(locale === 'en-US' ? 'en-US' : 'zh-CN', {
    dateStyle: 'medium',
    timeStyle: 'medium',
  }).format(timestamp);
}
