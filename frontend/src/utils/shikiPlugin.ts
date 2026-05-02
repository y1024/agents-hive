import type { BundledLanguage, CodeHighlighterPlugin, ThemeInput } from 'streamdown';
import { highlightCode } from './shikiHighlight';
import { isSupportedShikiLanguage, normalizeShikiLanguage, SUPPORTED_SHIKI_LANGUAGES } from './shikiLanguages';

type HighlightResult = Parameters<
  Parameters<CodeHighlighterPlugin['highlight']>[1] & ((r: never) => void)
>[0];

const SUPPORTED_LANGS = SUPPORTED_SHIKI_LANGUAGES as unknown as BundledLanguage[];
const THEMES: [ThemeInput, ThemeInput] = ['github-light', 'github-dark'];

export const SHIKI_PLUGIN: CodeHighlighterPlugin = {
  name: 'shiki',
  type: 'code-highlighter',
  getThemes: () => THEMES,
  getSupportedLanguages: () => SUPPORTED_LANGS,
  supportsLanguage: (language) => isSupportedShikiLanguage(language),
  highlight: (options, callback) => {
    const wrapped = callback
      ? (t: { tokens: unknown[][]; bg: string; fg: string }) =>
          callback({
            tokens: t.tokens as HighlightResult['tokens'],
            bg: t.bg,
            fg: t.fg,
          })
      : undefined;
    const language = normalizeShikiLanguage(options.language);
    const sync = highlightCode(
      options.code,
      language,
      wrapped,
    );
    if (!sync) return null;
    return {
      tokens: sync.tokens as HighlightResult['tokens'],
      bg: sync.bg,
      fg: sync.fg,
    };
  },
};
