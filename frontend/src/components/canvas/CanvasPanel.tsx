import { useCallback, useState, useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { X, Download, Copy, Check, Eye, Code, PanelRightClose, PanelRightOpen } from 'lucide-react';
import { useCanvasStore } from '../../store/canvas';
import type { Artifact } from '../../store/canvas';
import { HtmlRenderer } from './renderers/HtmlRenderer';
import { MarkdownRenderer } from './renderers/MarkdownRenderer';
import { JsonRenderer } from './renderers/JsonRenderer';
import { CodeRenderer } from './renderers/CodeRenderer';

export function CanvasPanel() {
  const { t } = useTranslation();
  const artifacts = useCanvasStore((s) => s.artifacts);
  const activeId = useCanvasStore((s) => s.activeId);
  const activeTab = useCanvasStore((s) => s.activeTab);
  const setActiveId = useCanvasStore((s) => s.setActiveId);
  const setActiveTab = useCanvasStore((s) => s.setActiveTab);
  const closeArtifact = useCanvasStore((s) => s.closeArtifact);
  const closeAll = useCanvasStore((s) => s.closeAll);

  const [copied, setCopied] = useState(false);
  const [collapsed, setCollapsed] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => () => clearTimeout(timerRef.current), []);

  useEffect(() => {
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !document.querySelector('.fixed.z-50')) {
        closeAll();
      }
    };
    document.addEventListener('keydown', handleKey);
    return () => document.removeEventListener('keydown', handleKey);
  }, [closeAll]);

  const active = artifacts.find((a) => a.id === activeId) || null;

  const handleCopy = useCallback(async () => {
    const { artifacts: arts, activeId: aid } = useCanvasStore.getState();
    const current = arts.find((a) => a.id === aid);
    if (!current) return;
    await navigator.clipboard.writeText(current.content);
    setCopied(true);
    clearTimeout(timerRef.current);
    timerRef.current = setTimeout(() => setCopied(false), 2000);
  }, []);

  const handleDownload = useCallback(() => {
    const { artifacts: arts, activeId: aid } = useCanvasStore.getState();
    const current = arts.find((a) => a.id === aid);
    if (!current) return;
    const { ext, mime } = getFileInfo(current);
    const blob = new Blob([current.content], { type: mime });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${current.title.replace(/[^a-zA-Z0-9_\-\u4e00-\u9fff]/g, '_')}${ext}`;
    a.click();
    URL.revokeObjectURL(url);
  }, []);

  if (!active) return null;

  if (collapsed) {
    return (
      <aside className="canvas-panel todos-panel-enter flex h-full w-8 shrink-0 flex-col border-l border-[var(--border-color)] bg-[var(--bg-card)]">
        <button
          type="button"
          onClick={() => setCollapsed(false)}
          className="flex h-full min-h-32 w-8 flex-col items-center justify-between gap-2 py-2 text-[var(--text-secondary)] transition hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
          aria-label={t('canvas.expand')}
          aria-expanded="false"
        >
          <PanelRightOpen className="h-4 w-4 shrink-0" />
          <span className="writing-vertical text-[10px] font-semibold uppercase tracking-[0.14em] text-[var(--text-secondary)]">
            Canvas
          </span>
          <span className="rounded-full bg-[var(--accent-subtle)] px-1.5 py-0.5 text-[10px] font-semibold text-[var(--accent-600)] dark:text-[var(--accent-300)]">
            {artifacts.length}
          </span>
        </button>
      </aside>
    );
  }

  return (
    <aside className="canvas-panel todos-panel-enter w-full flex flex-col border-l border-[var(--border-color)] bg-[var(--bg-primary)] h-full">
      {/* 单层 header：左边 artifact 标签，右边预览/源码切换 + 操作 */}
      <div className="flex items-center border-b border-[var(--border-color)] bg-[var(--bg-card)] shrink-0 h-10 px-1 gap-1">
        {/* Artifact 标签 */}
        <div role="tablist" className="flex items-center flex-1 min-w-0 overflow-x-auto h-full">
          {artifacts.map((a) => (
            <div
              key={a.id}
              role="tab"
              tabIndex={0}
              aria-selected={a.id === activeId}
              className={`flex items-center gap-1.5 px-3 h-full text-xs cursor-pointer border-r border-[var(--border-color)] shrink-0 max-w-[160px] transition-colors ${
                a.id === activeId
                  ? 'bg-[var(--bg-primary)] text-[var(--text-primary)] font-medium'
                  : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]'
              }`}
              onClick={() => setActiveId(a.id)}
              onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setActiveId(a.id); } }}
            >
              <span className="truncate">{a.title}</span>
              <button
                onClick={(e) => { e.stopPropagation(); closeArtifact(a.id); }}
                className="p-0.5 rounded hover:bg-[var(--bg-secondary)] shrink-0 opacity-50 hover:opacity-100 transition-opacity"
                aria-label={t('canvas.close')}
              >
                <X className="w-3 h-3" />
              </button>
            </div>
          ))}
        </div>

        {/* 右侧：预览/源码切换 + 操作按钮 */}
        <div className="flex items-center gap-0.5 px-1 shrink-0">
          {/* 预览/源码切换 — 分段控件样式 */}
          <div className="flex items-center bg-[var(--bg-secondary)] rounded-md p-0.5 mr-1">
            <button
              onClick={() => setActiveTab('preview')}
              className={`flex items-center gap-1 px-2 py-0.5 rounded text-xs transition-colors ${
                activeTab === 'preview'
                  ? 'bg-[var(--bg-card)] text-[var(--text-primary)] shadow-sm font-medium'
                  : 'text-[var(--text-secondary)] hover:text-[var(--text-primary)]'
              }`}
            >
              <Eye className="w-3 h-3" />
              <span>{t('canvas.preview')}</span>
            </button>
            <button
              onClick={() => setActiveTab('code')}
              className={`flex items-center gap-1 px-2 py-0.5 rounded text-xs transition-colors ${
                activeTab === 'code'
                  ? 'bg-[var(--bg-card)] text-[var(--text-primary)] shadow-sm font-medium'
                  : 'text-[var(--text-secondary)] hover:text-[var(--text-primary)]'
              }`}
            >
              <Code className="w-3 h-3" />
              <span>{t('canvas.code')}</span>
            </button>
          </div>

          {/* 操作按钮 */}
          <button
            onClick={handleCopy}
            className="p-1.5 rounded text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors"
            title={t('canvas.copyAll')}
          >
            {copied ? <Check className="w-3.5 h-3.5 text-emerald-500" /> : <Copy className="w-3.5 h-3.5" />}
          </button>
          <button
            onClick={handleDownload}
            className="p-1.5 rounded text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors"
            title={t('canvas.download')}
          >
            <Download className="w-3.5 h-3.5" />
          </button>
          <button
            onClick={() => setCollapsed(true)}
            className="p-1.5 rounded text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors"
            title={t('canvas.collapse')}
            aria-label={t('canvas.collapse')}
            aria-expanded="true"
          >
            <PanelRightClose className="w-3.5 h-3.5" />
          </button>
          <button
            onClick={closeAll}
            className="p-1.5 rounded text-[var(--text-secondary)] hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20 transition-colors"
            title={t('canvas.close')}
          >
            <X className="w-3.5 h-3.5" />
          </button>
        </div>
      </div>

      {/* 内容区 */}
      <div className="flex-1 min-h-0 overflow-hidden">
        {activeTab === 'preview' ? (
          <PreviewContent
            artifact={active}
            noPreviewText={t('canvas.noPreview')}
            switchToCodeText={t('canvas.code')}
            onSwitchToCode={() => setActiveTab('code')}
          />
        ) : (
          <CodeRenderer content={active.content} language={active.language} />
        )}
      </div>
    </aside>
  );
}

/* ===== 预览内容路由 ===== */
function PreviewContent({ artifact, noPreviewText, switchToCodeText, onSwitchToCode }: {
  artifact: Artifact;
  noPreviewText: string;
  switchToCodeText: string;
  onSwitchToCode: () => void;
}) {
  switch (artifact.type) {
    case 'html':
      return <HtmlRenderer content={artifact.content} />;
    case 'markdown':
      return <MarkdownRenderer content={artifact.content} />;
    case 'json':
      return <JsonRenderer content={artifact.content} />;
    case 'ppt':
      return <MarkdownRenderer content={artifact.content} />;
    case 'code':
      return <CodeRenderer content={artifact.content} language={artifact.language} />;
    default:
      return (
        <div className="flex flex-col items-center justify-center h-full text-[var(--text-secondary)] text-sm gap-3">
          <Code className="w-8 h-8 opacity-40" />
          <span>{noPreviewText}</span>
          <button
            onClick={onSwitchToCode}
            className="px-3 py-1.5 rounded-md text-xs bg-[var(--bg-secondary)] hover:bg-[var(--bg-hover)] text-[var(--text-primary)] transition-colors"
          >
            {switchToCodeText}
          </button>
        </div>
      );
  }
}

/* ===== 文件信息（扩展名 + MIME type） ===== */
function getFileInfo(artifact: Artifact): { ext: string; mime: string } {
  const map: Record<string, { ext: string; mime: string }> = {
    html: { ext: '.html', mime: 'text/html;charset=utf-8' },
    markdown: { ext: '.md', mime: 'text/markdown;charset=utf-8' },
    json: { ext: '.json', mime: 'application/json;charset=utf-8' },
    svg: { ext: '.svg', mime: 'image/svg+xml;charset=utf-8' },
    csv: { ext: '.csv', mime: 'text/csv;charset=utf-8' },
    mermaid: { ext: '.mmd', mime: 'text/plain;charset=utf-8' },
    ppt: { ext: '.md', mime: 'text/markdown;charset=utf-8' },
  };
  return map[artifact.type] || { ext: `.${artifact.language || 'txt'}`, mime: 'text/plain;charset=utf-8' };
}
