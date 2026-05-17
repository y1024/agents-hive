import { useState, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { FileText, Eye, Copy, Check, Code, Globe, Presentation } from 'lucide-react';
import { Streamdown } from 'streamdown';
import { ALLOWED_TAGS, STREAMDOWN_PREVIEW_PLUGINS } from '../../utils/streamdownConfig';

interface ArtifactCardProps {
  title: string;
  artifactType: 'markdown' | 'html' | 'code' | 'ppt';
  content: string;
  language?: string;
  isLoading?: boolean;
  onOpenCanvas: () => void;
}

function typeIcon(artifactType: ArtifactCardProps['artifactType']) {
  switch (artifactType) {
    case 'html': return <Globe className="w-3.5 h-3.5" />;
    case 'code': return <Code className="w-3.5 h-3.5" />;
    case 'ppt': return <Presentation className="w-3.5 h-3.5" />;
    default: return <FileText className="w-3.5 h-3.5" />;
  }
}

function typeLabel(artifactType: ArtifactCardProps['artifactType'], t: (k: string) => string) {
  return t(`chat.artifactType.${artifactType}`);
}

export function ArtifactCard({ title, artifactType, content, language, isLoading, onOpenCanvas }: ArtifactCardProps) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);

  // code 类型 artifact 的 content 是裸代码字符串，需要包 fenced block 才能被 shiki 高亮
  const previewSource = useMemo(() => {
    if (artifactType !== 'code') return content;
    const lang = (language || 'text').replace(/[^a-zA-Z0-9+#-]/g, '');
    return `\`\`\`${lang}\n${content}\n\`\`\``;
  }, [artifactType, content, language]);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(content);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // ignore
    }
  };

  return (
    <div className="artifact-card rounded-2xl border border-[var(--border-color)] bg-[var(--bg-card)] hover:border-[var(--accent-border)] transition-colors overflow-hidden my-2">
      {/* Header */}
      <div className="flex items-center gap-2 px-3 py-2.5 border-b border-[var(--border-color)]">
        <span className="text-[var(--accent-600)] dark:text-[var(--accent-300)] shrink-0">
          {typeIcon(artifactType)}
        </span>
        <span className="text-[13px] font-semibold text-[var(--text-primary)] flex-1 truncate">{title}</span>
        <span className="text-[11px] text-[var(--text-secondary)] shrink-0">
          {isLoading ? t('chat.generating') : typeLabel(artifactType, t)}
        </span>
      </div>

      {/* Preview area */}
      {isLoading ? (
        <div className="px-3 py-3 space-y-2">
          <div className="h-3 rounded animate-pulse bg-[var(--bg-secondary)] w-3/4" />
          <div className="h-3 rounded animate-pulse bg-[var(--bg-secondary)] w-1/2" />
          <div className="h-3 rounded animate-pulse bg-[var(--bg-secondary)] w-2/3" />
        </div>
      ) : (
        <div
          className="artifact-card-preview markdown-prose prose prose-sm max-w-none compact px-3 py-2 max-h-[120px] overflow-hidden text-[12px] leading-[1.5] dark:prose-invert"
          style={{ maskImage: 'linear-gradient(to bottom, black 70%, transparent)' }}
        >
          <Streamdown plugins={STREAMDOWN_PREVIEW_PLUGINS} allowedTags={ALLOWED_TAGS}>
            {previewSource}
          </Streamdown>
        </div>
      )}

      {/* Action bar */}
      {!isLoading && (
        <div className="flex items-center justify-between px-3 py-2 border-t border-[var(--border-color)]">
          <button
            onClick={onOpenCanvas}
            className="flex items-center gap-1.5 text-[11px] text-[var(--accent)] hover:text-[var(--accent-700)] transition-colors"
          >
            <Eye className="w-3 h-3" />
            {t('chat.viewFull')}
          </button>
          <button
            onClick={handleCopy}
            title={copied ? t('chat.copied') : t('chat.copy')}
            className="flex items-center gap-1 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors"
          >
            {copied ? <Check className="w-3 h-3" /> : <Copy className="w-3 h-3" />}
            {copied ? t('chat.copied') : t('chat.copy')}
          </button>
        </div>
      )}
    </div>
  );
}
