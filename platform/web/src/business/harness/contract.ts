type JsonRecord = Record<string, unknown>;

export interface HarnessOverview {
  activeEndpoints: number;
  activeSessions: number;
  conflictFrames: number;
  pendingEnrollments: number;
  unacknowledgedFrames: number;
}

export interface HarnessEnrollment {
  approvedAt?: string;
  consumedAt?: string;
  createdAt: string;
  endpointId?: string;
  endpointName: string;
  endpointType: string;
  expiresAt: string;
  id: string;
  status: string;
}

export interface HarnessEndpoint {
  createdAt: string;
  id: string;
  kemJkt: string;
  lastSeenAt?: string;
  name: string;
  platformName: string;
  revokedAt?: string;
  signingJkt: string;
  softwareVersion: string;
  status: string;
  type: string;
}

export interface HarnessSession {
  abaEndpointId: string;
  closedAt?: string;
  createdAt: string;
  hcEndpointId: string;
  id: string;
  keyGeneration: number;
  lastActivityAt?: string;
  requestedCapabilities: string[];
  runtimeProfileId: string;
  status: string;
  workspaceId: string;
}

export interface HarnessFrame {
  acknowledgedAt?: string;
  channelId: string;
  ciphertextBytes: number;
  direction: HarnessDirection;
  keyGeneration: number;
  messageId: string;
  receivedAt: string;
  receiverEndpointId: string;
  senderEndpointId: string;
  sequence: number;
  status: string;
}

export interface HarnessAck {
  direction: HarnessDirection;
  highestContiguousSequence: number;
  keyGeneration: number;
  receiverEndpointId: string;
  senderEndpointId: string;
  updatedAt: string;
}

export interface HarnessDelivery {
  acks: HarnessAck[];
  frames: HarnessFrame[];
  session: HarnessSession;
}

export type HarnessDirection = 'ABA_TO_HC' | 'HC_TO_ABA';

function record(value: unknown, label: string): JsonRecord {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new Error(`${label} is invalid`);
  }
  return value as JsonRecord;
}

function stringValue(value: unknown, label: string): string {
  if (typeof value !== 'string' || !value.trim()) throw new Error(`${label} is invalid`);
  return value;
}

function optionalString(value: unknown, label: string): string | undefined {
  if (value === null || value === undefined || value === '') return undefined;
  return stringValue(value, label);
}

function countValue(value: unknown, label: string): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) {
    throw new Error(`${label} is invalid`);
  }
  return value;
}

function directionValue(value: unknown): HarnessDirection {
  switch (value) {
    case 1:
      return 'HC_TO_ABA';
    case 2:
      return 'ABA_TO_HC';
    default:
      throw new Error('direction is invalid');
  }
}

function listValue<T>(value: unknown, label: string, parse: (item: unknown) => T): T[] {
  if (!Array.isArray(value) || value.length > 200) throw new Error(`${label} is invalid`);
  return value.map(parse);
}

function stringList(value: unknown, label: string): string[] {
  if (!Array.isArray(value) || value.length > 64) throw new Error(`${label} is invalid`);
  return value.map((item) => stringValue(item, label));
}

export function parseHarnessOverview(value: unknown): HarnessOverview {
  const source = record(value, 'Harness overview');
  return {
    activeEndpoints: countValue(source.activeEndpoints, 'activeEndpoints'),
    activeSessions: countValue(source.activeSessions, 'activeSessions'),
    conflictFrames: countValue(source.conflictFrames, 'conflictFrames'),
    pendingEnrollments: countValue(source.pendingEnrollments, 'pendingEnrollments'),
    unacknowledgedFrames: countValue(source.unacknowledgedFrames, 'unacknowledgedFrames'),
  };
}

export function parseHarnessEnrollment(value: unknown): HarnessEnrollment {
  const source = record(value, 'Harness enrollment');
  return {
    approvedAt: optionalString(source.approvedAt, 'approvedAt'),
    consumedAt: optionalString(source.consumedAt, 'consumedAt'),
    createdAt: stringValue(source.createdAt, 'createdAt'),
    endpointId: optionalString(source.endpointId, 'endpointId'),
    endpointName: stringValue(source.endpointName, 'endpointName'),
    endpointType: stringValue(source.endpointType, 'endpointType'),
    expiresAt: stringValue(source.expiresAt, 'expiresAt'),
    id: stringValue(source.id, 'id'),
    status: stringValue(source.status, 'status'),
  };
}

export function parseHarnessEndpoint(value: unknown): HarnessEndpoint {
  const source = record(value, 'Harness endpoint');
  return {
    createdAt: stringValue(source.createdAt, 'createdAt'),
    id: stringValue(source.id, 'id'),
    kemJkt: stringValue(source.kemJkt, 'kemJkt'),
    lastSeenAt: optionalString(source.lastSeenAt, 'lastSeenAt'),
    name: stringValue(source.name, 'name'),
    platformName: stringValue(source.platformName, 'platformName'),
    revokedAt: optionalString(source.revokedAt, 'revokedAt'),
    signingJkt: stringValue(source.signingJkt, 'signingJkt'),
    softwareVersion: stringValue(source.softwareVersion, 'softwareVersion'),
    status: stringValue(source.status, 'status'),
    type: stringValue(source.type, 'type'),
  };
}

export function parseHarnessSession(value: unknown): HarnessSession {
  const source = record(value, 'Harness session');
  return {
    abaEndpointId: stringValue(source.abaEndpointId, 'abaEndpointId'),
    closedAt: optionalString(source.closedAt, 'closedAt'),
    createdAt: stringValue(source.createdAt, 'createdAt'),
    hcEndpointId: stringValue(source.hcEndpointId, 'hcEndpointId'),
    id: stringValue(source.id, 'id'),
    keyGeneration: countValue(source.keyGeneration, 'keyGeneration'),
    lastActivityAt: optionalString(source.lastActivityAt, 'lastActivityAt'),
    requestedCapabilities: stringList(source.requestedCapabilities, 'requestedCapabilities'),
    runtimeProfileId: stringValue(source.runtimeProfileId, 'runtimeProfileId'),
    status: stringValue(source.status, 'status'),
    workspaceId: stringValue(source.workspaceId, 'workspaceId'),
  };
}

function parseHarnessFrame(value: unknown): HarnessFrame {
  const source = record(value, 'Harness frame');
  return {
    acknowledgedAt: optionalString(source.acknowledgedAt, 'acknowledgedAt'),
    channelId: stringValue(source.channelId, 'channelId'),
    ciphertextBytes: countValue(source.ciphertextBytes, 'ciphertextBytes'),
    direction: directionValue(source.direction),
    keyGeneration: countValue(source.keyGeneration, 'keyGeneration'),
    messageId: stringValue(source.messageId, 'messageId'),
    receivedAt: stringValue(source.receivedAt, 'receivedAt'),
    receiverEndpointId: stringValue(source.receiverEndpointId, 'receiverEndpointId'),
    senderEndpointId: stringValue(source.senderEndpointId, 'senderEndpointId'),
    sequence: countValue(source.sequence, 'sequence'),
    status: stringValue(source.status, 'status'),
  };
}

function parseHarnessAck(value: unknown): HarnessAck {
  const source = record(value, 'Harness ACK');
  return {
    direction: directionValue(source.direction),
    highestContiguousSequence: countValue(
      source.highestContiguousSequence,
      'highestContiguousSequence',
    ),
    keyGeneration: countValue(source.keyGeneration, 'keyGeneration'),
    receiverEndpointId: stringValue(source.receiverEndpointId, 'receiverEndpointId'),
    senderEndpointId: stringValue(source.senderEndpointId, 'senderEndpointId'),
    updatedAt: stringValue(source.updatedAt, 'updatedAt'),
  };
}

export function parseHarnessDelivery(value: unknown): HarnessDelivery {
  const source = record(value, 'Harness delivery');
  return {
    acks: listValue(source.acks, 'Harness ACK list', parseHarnessAck),
    frames: listValue(source.frames, 'Harness frame list', parseHarnessFrame),
    session: parseHarnessSession(source.session),
  };
}

export function parseHarnessEnrollmentList(value: unknown): HarnessEnrollment[] {
  return listValue(
    record(value, 'Harness enrollment page').items,
    'Harness enrollments',
    parseHarnessEnrollment,
  );
}

export function parseHarnessEndpointList(value: unknown): HarnessEndpoint[] {
  return listValue(
    record(value, 'Harness endpoint page').items,
    'Harness endpoints',
    parseHarnessEndpoint,
  );
}

export function parseHarnessSessionList(value: unknown): HarnessSession[] {
  return listValue(
    record(value, 'Harness session page').items,
    'Harness sessions',
    parseHarnessSession,
  );
}
