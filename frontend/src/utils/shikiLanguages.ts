import type { LanguageInput } from 'shiki/core';

const javascript = () => import('shiki/langs/javascript.mjs');
const typescript = () => import('shiki/langs/typescript.mjs');
const jsx = () => import('shiki/langs/jsx.mjs');
const tsx = () => import('shiki/langs/tsx.mjs');
const python = () => import('shiki/langs/python.mjs');
const go = () => import('shiki/langs/go.mjs');
const rust = () => import('shiki/langs/rust.mjs');
const java = () => import('shiki/langs/java.mjs');
const c = () => import('shiki/langs/c.mjs');
const csharp = () => import('shiki/langs/csharp.mjs');
const php = () => import('shiki/langs/php.mjs');
const swift = () => import('shiki/langs/swift.mjs');
const kotlin = () => import('shiki/langs/kotlin.mjs');
const scala = () => import('shiki/langs/scala.mjs');
const shellscript = () => import('shiki/langs/shellscript.mjs');
const fish = () => import('shiki/langs/fish.mjs');
const powershell = () => import('shiki/langs/powershell.mjs');
const html = () => import('shiki/langs/html.mjs');
const xml = () => import('shiki/langs/xml.mjs');
const css = () => import('shiki/langs/css.mjs');
const scss = () => import('shiki/langs/scss.mjs');
const sass = () => import('shiki/langs/sass.mjs');
const less = () => import('shiki/langs/less.mjs');
const json = () => import('shiki/langs/json.mjs');
const yaml = () => import('shiki/langs/yaml.mjs');
const toml = () => import('shiki/langs/toml.mjs');
const ini = () => import('shiki/langs/ini.mjs');
const csv = () => import('shiki/langs/csv.mjs');
const sql = () => import('shiki/langs/sql.mjs');
const graphql = () => import('shiki/langs/graphql.mjs');
const protobuf = () => import('shiki/langs/protobuf.mjs');
const markdown = () => import('shiki/langs/markdown.mjs');
const diff = () => import('shiki/langs/diff.mjs');
const docker = () => import('shiki/langs/docker.mjs');
const make = () => import('shiki/langs/make.mjs');
const cmake = () => import('shiki/langs/cmake.mjs');
const lua = () => import('shiki/langs/lua.mjs');
const perl = () => import('shiki/langs/perl.mjs');
const r = () => import('shiki/langs/r.mjs');
const dart = () => import('shiki/langs/dart.mjs');
const elixir = () => import('shiki/langs/elixir.mjs');
const erlang = () => import('shiki/langs/erlang.mjs');
const clojure = () => import('shiki/langs/clojure.mjs');
const haskell = () => import('shiki/langs/haskell.mjs');
const ocaml = () => import('shiki/langs/ocaml.mjs');
const fsharp = () => import('shiki/langs/fsharp.mjs');
const groovy = () => import('shiki/langs/groovy.mjs');

export const SHIKI_LANGUAGE_LOADERS = {
  javascript,
  typescript,
  jsx,
  tsx,
  python,
  go,
  rust,
  java,
  c,
  csharp,
  php,
  swift,
  kotlin,
  scala,
  shellscript,
  fish,
  powershell,
  html,
  xml,
  css,
  scss,
  sass,
  less,
  json,
  yaml,
  toml,
  ini,
  csv,
  sql,
  graphql,
  protobuf,
  markdown,
  diff,
  docker,
  make,
  cmake,
  lua,
  perl,
  r,
  dart,
  elixir,
  erlang,
  clojure,
  haskell,
  ocaml,
  fsharp,
  groovy,
} satisfies Record<string, LanguageInput>;

export type LoadableShikiLanguage = keyof typeof SHIKI_LANGUAGE_LOADERS;
export type SupportedShikiLanguage = LoadableShikiLanguage | 'text';

const LANGUAGE_ALIASES = {
  '': 'text',
  text: 'text',
  txt: 'text',
  plain: 'text',
  plaintext: 'text',
  javascript: 'javascript',
  js: 'javascript',
  cjs: 'javascript',
  mjs: 'javascript',
  typescript: 'typescript',
  ts: 'typescript',
  jsx: 'jsx',
  tsx: 'tsx',
  python: 'python',
  py: 'python',
  go: 'go',
  golang: 'go',
  rust: 'rust',
  rs: 'rust',
  java: 'java',
  c: 'c',
  cpp: 'c',
  'c++': 'c',
  cxx: 'c',
  csharp: 'csharp',
  cs: 'csharp',
  'c#': 'csharp',
  ruby: 'text',
  rb: 'text',
  php: 'php',
  swift: 'swift',
  kotlin: 'kotlin',
  kt: 'kotlin',
  scala: 'scala',
  bash: 'shellscript',
  sh: 'shellscript',
  shell: 'shellscript',
  shellscript: 'shellscript',
  zsh: 'shellscript',
  fish: 'fish',
  powershell: 'powershell',
  ps: 'powershell',
  ps1: 'powershell',
  html: 'html',
  xml: 'xml',
  svg: 'xml',
  css: 'css',
  scss: 'scss',
  sass: 'sass',
  less: 'less',
  json: 'json',
  yaml: 'yaml',
  yml: 'yaml',
  toml: 'toml',
  ini: 'ini',
  csv: 'csv',
  sql: 'sql',
  graphql: 'graphql',
  gql: 'graphql',
  protobuf: 'protobuf',
  proto: 'protobuf',
  markdown: 'markdown',
  md: 'markdown',
  diff: 'diff',
  patch: 'diff',
  docker: 'docker',
  dockerfile: 'docker',
  make: 'make',
  makefile: 'make',
  cmake: 'cmake',
  lua: 'lua',
  perl: 'perl',
  r: 'r',
  dart: 'dart',
  elixir: 'elixir',
  erlang: 'erlang',
  clojure: 'clojure',
  clj: 'clojure',
  haskell: 'haskell',
  hs: 'haskell',
  ocaml: 'ocaml',
  fsharp: 'fsharp',
  fs: 'fsharp',
  groovy: 'groovy',
} as const satisfies Record<string, SupportedShikiLanguage>;

export type SupportedShikiLanguageId = keyof typeof LANGUAGE_ALIASES;

export const SUPPORTED_SHIKI_LANGUAGES = Object.keys(
  LANGUAGE_ALIASES,
) as SupportedShikiLanguageId[];

export function normalizeShikiLanguage(language: string | null | undefined): SupportedShikiLanguage {
  const key = (language ?? '').trim().toLowerCase();
  return LANGUAGE_ALIASES[key as SupportedShikiLanguageId] ?? 'text';
}

export function isSupportedShikiLanguage(language: string): boolean {
  return Object.prototype.hasOwnProperty.call(
    LANGUAGE_ALIASES,
    language.trim().toLowerCase(),
  );
}
