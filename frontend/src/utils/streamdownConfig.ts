import type { AllowedTags, CustomRenderer, MathPlugin } from 'streamdown';
import remarkMath from 'remark-math';
import rehypeKatex from 'rehype-katex';
import { BusinessCodeRenderer } from '../components/chat/BusinessCodeRenderer';
import { SHIKI_PLUGIN } from './shikiPlugin';

export const MATH_PLUGIN: MathPlugin = {
  name: 'katex',
  type: 'math',
  remarkPlugin: remarkMath,
  rehypePlugin: rehypeKatex,
};


export const ALLOWED_TAGS: AllowedTags = {
  code: ['className'],
  pre: ['className'],
  span: ['className', 'style'],
  div: ['className', 'style'],
  img: ['src', 'alt', 'title'],
  math: ['xmlns', 'display'],
  annotation: ['encoding'],
  semantics: [],
  mrow: [],
  mn: [],
  mi: [],
  mo: [],
  mtext: [],
  mstyle: [],
  mark: [],
  msup: [],
  msub: [],
  msubsup: [],
  mfrac: [],
  mspace: [],
  mover: [],
  munder: [],
  munderover: [],
  mpadded: [],
  mphantom: [],
  mroot: [],
  msqrt: [],
  mtable: [],
  mtr: [],
  mtd: [],
};

const BUSINESS_RENDERER_LANGS = [
  '',
  'js', 'javascript', 'ts', 'typescript', 'jsx', 'tsx',
  'py', 'python',
  'go', 'golang',
  'rust', 'java',
  'c', 'cpp', 'cs', 'csharp',
  'rb', 'ruby', 'php', 'swift', 'kotlin', 'scala',
  'bash', 'sh', 'shell', 'zsh', 'fish', 'powershell', 'ps1',
  'html', 'xml', 'svg',
  'css', 'scss', 'sass', 'less',
  'json', 'yaml', 'yml', 'toml', 'ini', 'csv',
  'sql', 'graphql', 'proto', 'protobuf',
  'md', 'markdown', 'diff',
  'dockerfile', 'makefile', 'cmake',
  'lua', 'perl', 'r', 'dart', 'elixir', 'erlang',
  'clojure', 'haskell', 'ocaml', 'fsharp',
  'beanshell', 'groovy',
  'plaintext', 'text', 'txt',
];

export const CUSTOM_RENDERERS: CustomRenderer[] = [
  { language: BUSINESS_RENDERER_LANGS, component: BusinessCodeRenderer },
];

export const STREAMDOWN_PLUGINS = {
  math: MATH_PLUGIN,
  code: SHIKI_PLUGIN,
  renderers: CUSTOM_RENDERERS,
} as const;

// 预览区专用：只保留 shiki 高亮 + math，不挂 BusinessCodeRenderer 外壳
// （ArtifactCard 的 max-h 预览区塞不下带 header/action bar 的完整代码块）
export const STREAMDOWN_PREVIEW_PLUGINS = {
  math: MATH_PLUGIN,
  code: SHIKI_PLUGIN,
} as const;
