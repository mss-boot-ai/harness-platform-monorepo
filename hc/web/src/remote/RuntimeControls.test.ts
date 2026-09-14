import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { RuntimeConfiguration } from './RuntimeControls';
import { newRuntimeState, readDescriptor } from './runtime-state';

it('renders only execution-side configuration and a truthful unavailable context state', () => {
  const state = readDescriptor(newRuntimeState(), { initialize: { protocolVersion: 1 }, bridge: { protocolVersion: 1, processEpoch: '1', duplex: true, turnCancellation: true }, session: { sessionId: 's', configOptions: [{ id: 'reasoning', name: 'Reasoning', category: 'thought_level', type: 'select', currentValue: 'low', options: [{ value: 'low', name: 'Low' }, { value: 'high', name: 'High' }] }] } }, 's');
  const output = renderToStaticMarkup(createElement(RuntimeConfiguration, { state, pending: null, disabled: true, onChange: () => undefined, onRefresh: () => undefined }));
  expect(output).toContain('推理强度'); expect(output).toContain('disabled'); expect(output).toContain('运行时尚未上报');
  expect(output).not.toContain('gpt-'); expect(output).not.toContain('100%');
});
