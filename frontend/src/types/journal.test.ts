import { describe, expect, it } from 'vitest';
import { getCharacterState, journalToolDisplayName, type JournalEvent } from './journal';

function toolEvent(toolName: string, args?: string): JournalEvent {
  return {
    type: 'tool_call',
    timestamp: '2026-05-16T00:00:00Z',
    tool_name: toolName,
    arguments: args,
  };
}

describe('journal filesystem mixed action display', () => {
  it('displays filesystem action as tool.action', () => {
    expect(journalToolDisplayName(toolEvent('filesystem', '{"action":"grep"}'))).toBe('filesystem.grep');
    expect(journalToolDisplayName(toolEvent('filesystem', '{"action":"edit"}'))).toBe('filesystem.edit');
  });

  it('classifies filesystem read and write actions without treating malformed args as reading', () => {
    expect(getCharacterState(toolEvent('filesystem', '{"action":"read"}'))).toBe('reading');
    expect(getCharacterState(toolEvent('filesystem', '{"action":"grep"}'))).toBe('reading');
    expect(getCharacterState(toolEvent('filesystem', '{"action":"write"}'))).toBe('coding');
    expect(getCharacterState(toolEvent('filesystem', '{"action":"multiedit"}'))).toBe('coding');
    expect(getCharacterState(toolEvent('filesystem', '{not json'))).toBe('running');
    expect(getCharacterState(toolEvent('filesystem', '{"action":42}'))).toBe('running');
  });
});
