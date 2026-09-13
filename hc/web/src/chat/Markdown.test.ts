import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { Markdown } from './Markdown';
describe('safe chat Markdown', () => {
  it('renders code fences, headings, lists and inline code without executing HTML', () => {
    const output = renderToStaticMarkup(createElement(Markdown, { text: '# 方案\n\n- **检查** `state`\n\n```go\nfmt.Println("<script>")\n```' }));
    expect(output).toContain('<h3>'); expect(output).toContain('<ul>');
    expect(output).toContain('<strong>检查</strong>'); expect(output).toContain('复制代码');
    expect(output).toContain('&lt;script&gt;'); expect(output).not.toContain('<script>');
  });
  it('keeps raw HTML inert and never loads remote images', () => {
    const output = renderToStaticMarkup(createElement(Markdown, { text: '<img src=x onerror=alert(1)>\n\n![tracker](https://example.com/track)\n\n[x](javascript:alert)' }));
    expect(output).not.toContain('<img'); expect(output).not.toContain('href="javascript:');
    expect(output).toContain('&lt;img');
  });
  it('isolates opened external links and renders unfinished streamed fences', () => {
    const output = renderToStaticMarkup(createElement(Markdown, { text: '[docs](https://example.com)\n\n```sh\necho hello' }));
    expect(output).toContain('rel="noopener noreferrer"'); expect(output).toContain('<pre><code>echo hello</code></pre>');
  });
});
