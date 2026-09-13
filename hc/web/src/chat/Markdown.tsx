import { Fragment, useState, type ReactNode } from 'react';
import { Icon } from './Icon';
import { safeLink } from './model';
export function CopyButton({ text, label = '复制' }: { readonly text: string; readonly label?: string }) {
  const [status, setStatus] = useState<'idle' | 'copied' | 'failed'>('idle');
  const copy = async () => {
    try { await navigator.clipboard.writeText(text); setStatus('copied'); }
    catch { setStatus('failed'); }
  };
  return <button type="button" className="copy-button" onClick={() => void copy()} aria-label={status === 'copied' ? '已复制' : label}>
    <Icon name={status === 'copied' ? 'check' : 'copy'} /><span aria-live="polite">{status === 'copied' ? '已复制' : status === 'failed' ? '复制失败，请手动选择' : label}</span>
  </button>;
}
function inline(text: string): ReactNode[] {
  const result: ReactNode[] = [];
  const pattern = /(`[^`\n]+`|\*\*[^*\n]+\*\*|\[[^\]\n]+\]\([^\s)]+\))/gu;
  let cursor = 0;
  for (const match of text.matchAll(pattern)) {
    const index = match.index;
    result.push(text.slice(cursor, index));
    const token = match[0];
    if (token.startsWith('`')) result.push(<code key={index}>{token.slice(1, -1)}</code>);
    else if (token.startsWith('**')) result.push(<strong key={index}>{token.slice(2, -2)}</strong>);
    else {
      const separator = token.indexOf('](');
      const href = safeLink(token.slice(separator + 2, -1));
      result.push(href === null ? token : <a key={index} href={href} target="_blank" rel="noopener noreferrer">{token.slice(1, separator)}</a>);
    }
    cursor = index + token.length;
  }
  result.push(text.slice(cursor));
  return result;
}
/** Small, deliberately non-HTML Markdown subset. No remote images, raw HTML, eval, or innerHTML. */
export function Markdown({ text }: { readonly text: string }) {
  const lines = text.replace(/\r\n/gu, '\n').split('\n');
  const blocks: ReactNode[] = [];
  let index = 0;
  while (index < lines.length) {
    const line = lines[index] ?? '';
    const key = index;
    if (line.startsWith('```')) {
      const language = line.slice(3).trim().slice(0, 32) || '代码';
      const code: string[] = [];
      index += 1;
      while (index < lines.length && !(lines[index] ?? '').startsWith('```')) code.push(lines[index++] ?? '');
      if (index < lines.length) index += 1;
      blocks.push(<div className="code-block" key={key}><div className="code-toolbar"><span>{language}</span><CopyButton text={code.join('\n')} label="复制代码" /></div><pre><code>{code.join('\n')}</code></pre></div>);
      continue;
    }
    if (line.trim() === '') { index += 1; continue; }
    const heading = /^(#{1,4})\s+(.+)$/u.exec(line);
    if (heading !== null) { blocks.push(<h3 key={key}>{inline(heading[2] ?? '')}</h3>); index += 1; continue; }
    if (/^\s*([-*]|\d+\.)\s/u.test(line)) {
      const ordered = /^\s*\d+\./u.test(line);
      const items: ReactNode[] = [];
      const pattern = ordered ? /^\s*\d+\.\s/u : /^\s*[-*]\s/u;
      while (index < lines.length && pattern.test(lines[index] ?? '')) {
        items.push(<li key={index}>{inline((lines[index] ?? '').replace(pattern, ''))}</li>); index += 1;
      }
      blocks.push(ordered ? <ol key={key}>{items}</ol> : <ul key={key}>{items}</ul>);
      continue;
    }
    if (line.startsWith('> ')) { blocks.push(<blockquote key={key}>{inline(line.slice(2))}</blockquote>); index += 1; continue; }
    const paragraph: string[] = [];
    do { paragraph.push(lines[index++] ?? ''); }
    while (index < lines.length && (lines[index] ?? '').trim() !== '' && !/^(?:```|#{1,4}\s|>\s|\s*[-*]\s|\s*\d+\.\s)/u.test(lines[index] ?? ''));
    blocks.push(<p key={key}>{paragraph.map((part, partIndex) => <Fragment key={partIndex}>{partIndex > 0 ? <br /> : null}{inline(part)}</Fragment>)}</p>);
  }
  return <div className="markdown-content">{blocks}</div>;
}
