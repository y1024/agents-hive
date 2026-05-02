import { useState, useEffect, useMemo } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import type { CustomRendererProps } from 'streamdown';
import { CodeBlock } from '@/components/ai-elements/code-block';
import {
  ChevronRight, ChevronDown,
  Copy, Check,
  Play, WrapText, Moon, Sun, Maximize2, Minimize2,
  ExternalLink,
} from 'lucide-react';
import { MermaidBlock } from './MermaidBlock';
import { useCanvasStore, detectArtifactType } from '../../store/canvas';
import type { Artifact } from '../../store/canvas';

function generateUUID(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0;
    return (c === 'x' ? r : (r & 0x3) | 0x8).toString(16);
  });
}

const LANG_DISPLAY: Record<string, string> = {
  js: 'JavaScript', javascript: 'JavaScript', ts: 'TypeScript', typescript: 'TypeScript',
  jsx: 'JSX', tsx: 'TSX',
  py: 'Python', python: 'Python', go: 'Go', golang: 'Go',
  bash: 'Bash', sh: 'Shell', zsh: 'Zsh', shell: 'Shell',
  java: 'Java', rust: 'Rust', rb: 'Ruby', ruby: 'Ruby',
  cpp: 'C++', c: 'C', cs: 'C#', csharp: 'C#',
  sql: 'SQL', json: 'JSON', yaml: 'YAML', yml: 'YAML',
  html: 'HTML', css: 'CSS', scss: 'SCSS', less: 'LESS',
  xml: 'XML', md: 'Markdown', markdown: 'Markdown',
  dockerfile: 'Dockerfile', toml: 'TOML', ini: 'INI', php: 'PHP',
  swift: 'Swift', kotlin: 'Kotlin', scala: 'Scala', r: 'R',
  beanshell: 'BeanShell', groovy: 'Groovy', lua: 'Lua', perl: 'Perl',
  powershell: 'PowerShell', ps1: 'PowerShell',
  graphql: 'GraphQL', proto: 'Protobuf', protobuf: 'Protobuf',
  makefile: 'Makefile', cmake: 'CMake',
  diff: 'Diff', plaintext: 'Text', text: 'Text', txt: 'Text',
};

const SCRIPT_LANGS = new Set([
  'bash', 'sh', 'shell', 'zsh', 'python', 'py',
  'beanshell', 'groovy', 'powershell', 'ps1',
  'ruby', 'rb', 'perl', 'lua',
]);

export function BusinessCodeRenderer({ code, language }: CustomRendererProps) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const [collapsed, setCollapsed] = useState(false);
  const [wordWrap, setWordWrap] = useState(false);
  const [darkTheme, setDarkTheme] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);

  const lang = (language || '').toLowerCase();
  const displayLang = LANG_DISPLAY[lang] || language || 'Code';
  const isScriptLang = SCRIPT_LANGS.has(lang);
  const lineCount = useMemo(() => code.split('\n').length, [code]);

  useEffect(() => {
    if (!fullscreen) return;
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setFullscreen(false);
    };
    document.addEventListener('keydown', handleKey);
    return () => document.removeEventListener('keydown', handleKey);
  }, [fullscreen]);

  if (lang === 'mermaid') {
    return <MermaidBlock code={code} />;
  }

  const handleCopy = async () => {
    await navigator.clipboard.writeText(code);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const handleOpenCanvas = () => {
    const type = detectArtifactType(language, code);
    const artifact: Artifact = {
      id: generateUUID(),
      title: displayLang,
      language,
      content: code,
      type,
    };
    useCanvasStore.getState().openArtifact(artifact);
  };

  const actionBar = (
    <>
      <span className="font-medium text-[11px] text-[var(--text-secondary)] mr-auto self-center">
        {displayLang}
        {lineCount > 1 && (
          <span className="ml-1.5 text-[10px] opacity-60">{lineCount} lines</span>
        )}
      </span>
      <button
        onClick={() => setCollapsed((v) => !v)}
        className="code-header-btn"
        title={collapsed ? t('chat.expandCode') : t('chat.collapseCode')}
        aria-label={collapsed ? t('chat.expandCode') : t('chat.collapseCode')}
      >
        {collapsed ? <ChevronRight className="w-3 h-3" /> : <ChevronDown className="w-3 h-3" />}
      </button>
      {isScriptLang && (
        <button className="code-header-btn" title={t('chat.run')} aria-label={t('chat.run')}>
          <Play className="w-3 h-3" />
        </button>
      )}
      <button
        className={`code-header-btn ${copied ? 'text-emerald-500' : ''}`}
        onClick={handleCopy}
        title={copied ? t('chat.copied') : t('chat.copyCode')}
        aria-label={t('chat.copyCode')}
      >
        {copied ? <Check className="w-3 h-3" /> : <Copy className="w-3 h-3" />}
      </button>
      <button
        className={`code-header-btn ${wordWrap ? 'text-[var(--accent-600)] dark:text-[var(--accent-300)]' : ''}`}
        onClick={() => setWordWrap((v) => !v)}
        title={t('chat.wrapLines')}
        aria-label={t('chat.wrapLines')}
      >
        <WrapText className="w-3 h-3" />
      </button>
      <button
        className={`code-header-btn ${darkTheme ? 'text-[var(--accent-600)] dark:text-[var(--accent-300)]' : ''}`}
        onClick={() => setDarkTheme((v) => !v)}
        title="Theme"
        aria-label="Toggle theme"
      >
        {darkTheme ? <Sun className="w-3 h-3" /> : <Moon className="w-3 h-3" />}
      </button>
      <button
        className="code-header-btn"
        onClick={() => setFullscreen((v) => !v)}
        title={t('chat.fullscreen')}
        aria-label={t('chat.fullscreen')}
      >
        {fullscreen ? <Minimize2 className="w-3 h-3" /> : <Maximize2 className="w-3 h-3" />}
      </button>
      <button
        className="code-header-btn"
        onClick={handleOpenCanvas}
        title={t('canvas.openInCanvas')}
        aria-label={t('canvas.openInCanvas')}
      >
        <ExternalLink className="w-3 h-3" />
      </button>
    </>
  );

  const wrapClass = [
    darkTheme ? 'code-block-dark-override' : '',
    wordWrap ? 'code-block-wrap' : '',
  ].filter(Boolean).join(' ');

  if (collapsed) {
    return (
      <div className={`group relative my-4 flex w-full flex-col gap-2 rounded-xl border border-border bg-sidebar p-2 ${wrapClass}`}>
        <div className="flex h-8 items-center text-muted-foreground text-xs px-2 gap-1">
          {actionBar}
        </div>
        <div
          className="px-4 py-2 text-xs text-[var(--text-secondary)] italic cursor-pointer"
          onClick={() => setCollapsed(false)}
        >
          {lineCount} {lineCount === 1 ? 'line' : 'lines'} — {t('chat.expandCode')}
        </div>
      </div>
    );
  }

  const body = (
    <CodeBlock code={code} language={lang || 'text'} className={wrapClass}>
      {actionBar}
    </CodeBlock>
  );

  if (fullscreen) {
    return (
      <>
        <div className={`group relative my-4 flex w-full flex-col gap-2 rounded-xl border border-border bg-sidebar p-2 ${wrapClass}`}>
          <div className="flex h-8 items-center text-muted-foreground text-xs px-2 gap-1">
            {actionBar}
          </div>
          <div className="px-4 py-2 text-xs text-[var(--text-secondary)] italic">
            {t('chat.fullscreen')} · Esc
          </div>
        </div>
        {createPortal(
          <>
            <div className="fixed inset-0 z-40 bg-black/50" onClick={() => setFullscreen(false)} />
            <div className="fixed inset-4 z-50 flex flex-col rounded-2xl overflow-hidden shadow-2xl bg-[var(--bg-primary)]">
              <div className="flex-1 overflow-auto">
                {body}
              </div>
            </div>
          </>,
          document.body,
        )}
      </>
    );
  }

  return body;
}
