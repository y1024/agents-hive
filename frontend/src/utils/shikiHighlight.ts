import {
  type ThemedToken,
  type HighlighterGeneric,
  createBundledHighlighter,
} from 'shiki/core';
import { createJavaScriptRegexEngine } from 'shiki/engine/javascript';
import { SHIKI_LANGUAGE_LOADERS, type LoadableShikiLanguage, type SupportedShikiLanguage } from './shikiLanguages';

// 使用 JS regex engine 而非 WebAssembly oniguruma，避免依赖 CSP 的 'wasm-unsafe-eval' 指令。
// 对高亮质量无影响；在大部分 TextMate 语法下完全兼容，且移除了整个 WASM 启动路径。
const createHighlighter = createBundledHighlighter({
  langs: SHIKI_LANGUAGE_LOADERS,
  themes: {
    'github-light': () => import('shiki/themes/github-light.mjs'),
    'github-dark': () => import('shiki/themes/github-dark.mjs'),
  },
  engine: () => createJavaScriptRegexEngine(),
});

type ShikiTheme = 'github-light' | 'github-dark';
type HighlightLanguage = LoadableShikiLanguage;

export interface TokenizedCode {
  tokens: ThemedToken[][];
  fg: string;
  bg: string;
}

const highlighterCache = new Map<
  string,
  Promise<HighlighterGeneric<LoadableShikiLanguage, ShikiTheme>>
>();

const tokensCache = new Map<string, TokenizedCode>();
const subscribers = new Map<string, Set<(result: TokenizedCode) => void>>();

const getTokensCacheKey = (code: string, language: SupportedShikiLanguage) => {
  const start = code.slice(0, 100);
  const end = code.length > 100 ? code.slice(-100) : '';
  return `${language}:${code.length}:${start}:${end}`;
};

const getHighlighter = (
  language: HighlightLanguage,
): Promise<HighlighterGeneric<LoadableShikiLanguage, ShikiTheme>> => {
  const cached = highlighterCache.get(language);
  if (cached) return cached;
  const p = createHighlighter({
    langs: [language],
    themes: ['github-light', 'github-dark'],
  });
  highlighterCache.set(language, p);
  return p;
};

export const createRawTokens = (code: string): TokenizedCode => ({
  bg: 'transparent',
  fg: 'inherit',
  tokens: code.split('\n').map((line) =>
    line === ''
      ? []
      : [
          {
            color: 'inherit',
            content: line,
          } as ThemedToken,
        ],
  ),
});

export const highlightCode = (
  code: string,
  language: SupportedShikiLanguage,
  callback?: (result: TokenizedCode) => void,
): TokenizedCode | null => {
  if (language === 'text') {
    return createRawTokens(code);
  }

  const key = getTokensCacheKey(code, language);
  const cached = tokensCache.get(key);
  if (cached) return cached;

  if (callback) {
    if (!subscribers.has(key)) subscribers.set(key, new Set());
    subscribers.get(key)?.add(callback);
  }

  getHighlighter(language)
    .then((h) => {
      const loaded = h.getLoadedLanguages();
      const lang = loaded.includes(language) ? language : 'text';
      const res = h.codeToTokens(code, {
        lang,
        themes: { dark: 'github-dark', light: 'github-light' },
      });
      const tokenized: TokenizedCode = {
        bg: res.bg ?? 'transparent',
        fg: res.fg ?? 'inherit',
        tokens: res.tokens,
      };
      tokensCache.set(key, tokenized);
      const subs = subscribers.get(key);
      if (subs) {
        for (const s of subs) s(tokenized);
        subscribers.delete(key);
      }
    })
    .catch((err) => {
      console.error('[shiki] highlight failed for lang=%s:', language, err);
      subscribers.delete(key);
    });

  return null;
};
