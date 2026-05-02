import { describe, expect, it } from 'vitest';
import { SHIKI_PLUGIN } from './shikiPlugin';

describe('SHIKI_PLUGIN language bundle', () => {
  it('supports common business languages without enabling unrelated large grammars', () => {
    expect(SHIKI_PLUGIN.supportsLanguage('typescript')).toBe(true);
    expect(SHIKI_PLUGIN.supportsLanguage('go')).toBe(true);
    expect(SHIKI_PLUGIN.supportsLanguage('markdown')).toBe(true);
    expect(SHIKI_PLUGIN.supportsLanguage('ruby')).toBe(true);
    expect(SHIKI_PLUGIN.supportsLanguage('emacs-lisp')).toBe(false);
    expect(SHIKI_PLUGIN.supportsLanguage('wasm')).toBe(false);
    expect(SHIKI_PLUGIN.supportsLanguage('wolfram')).toBe(false);
  });
});
