import { useEffect, useId, useRef, type ReactNode } from 'react';
import { Icon } from './Icon';
export function Dialog({ open, onClose, title, children }: {
  readonly open: boolean; readonly onClose: () => void; readonly title: string; readonly children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  useEffect(() => {
    const dialog = ref.current;
    if (dialog === null) return;
    if (open && !dialog.open) dialog.showModal();
    if (!open && dialog.open) dialog.close();
  }, [open]);
  // Children stay mounted: hiding settings must not tear down the gateway or discard session keys.
  return <dialog ref={ref} className="hc-dialog" aria-labelledby={titleId}
    onCancel={(event) => { event.preventDefault(); onClose(); }}
    onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}>
    <div className="dialog-surface">
      <header className="dialog-header"><h2 id={titleId}>{title}</h2><button className="icon-button" type="button" aria-label={`关闭${title}`} onClick={onClose}><Icon name="close" /></button></header>
      <div className="dialog-body">{children}</div>
    </div>
  </dialog>;
}
