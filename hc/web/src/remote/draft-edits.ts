import { MAX_CONVERSATIONS } from './conversation-store';
export interface DraftEdit { readonly text: string; readonly version: number; readonly failed: boolean }
export type DraftEdits = ReadonlyMap<string | null, DraftEdit>;
/** Unsaved text belongs to its conversation even when another conversation is being viewed or saved. */
export function editDraft(edits: DraftEdits, id: string | null, text: string, version: number): DraftEdits {
  if (text.length > 16_000 || !Number.isSafeInteger(version) || version < 1 || (!edits.has(id) && edits.size >= MAX_CONVERSATIONS + 1)) throw new Error('Pending draft bounds exceeded');
  const next = new Map(edits); next.set(id, { text, version, failed: false }); return next;
}
export function finishDraft(edits: DraftEdits, id: string | null, version: number, saved: boolean): DraftEdits {
  const value = edits.get(id); if (value?.version !== version) return edits;
  const next = new Map(edits);
  if (saved) next.delete(id); else next.set(id, { ...value, failed: true });
  return next;
}
