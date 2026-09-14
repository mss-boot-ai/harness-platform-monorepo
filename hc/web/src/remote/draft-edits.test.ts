import { editDraft, finishDraft } from './draft-edits';
it('retains a failed A edit when B finishes saving and the user switches back to A', () => {
  const both = editDraft(editDraft(new Map(), 'A', 'unsaved A', 1), 'B', 'saved B', 2);
  const failed = finishDraft(both, 'A', 1, false);
  const afterB = finishDraft(failed, 'B', 2, true);
  expect(afterB.get('A')).toEqual({ text: 'unsaved A', version: 1, failed: true }); expect(afterB.has('B')).toBe(false);
});
it('does not clear newer text or a new composition when an older send or save completes', () => {
  const current = editDraft(editDraft(new Map(), 'A', 'next A draft', 3), null, 'new composition', 4);
  expect(finishDraft(current, 'A', 1, true)).toBe(current);
  expect(finishDraft(current, null, 2, false)).toBe(current);
  expect(current.get('A')?.text).toBe('next A draft'); expect(current.get(null)?.text).toBe('new composition');
});
it('allows an explicit retry of failed text without retaining the old failure', () => {
  const failed = finishDraft(editDraft(new Map(), 'A', 'keep me', 1), 'A', 1, false);
  const retry = editDraft(failed, 'A', 'keep me', 2);
  expect(retry.get('A')?.failed).toBe(false); expect(finishDraft(retry, 'A', 2, true).size).toBe(0);
});
