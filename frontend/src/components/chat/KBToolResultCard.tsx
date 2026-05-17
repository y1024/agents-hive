import { useEffect, useMemo, useState } from 'react';
import { ImageIcon, FileText, ExternalLink } from 'lucide-react';
import type { KBAssetRef, KBSectionTextResult } from '../../types/api';
import { LocalNodeClient } from '../../api/node-client';

const assetClient = new LocalNodeClient();

interface KBToolResultCardProps {
  name: string;
  result?: string;
  sessionId?: string;
  domainId?: string;
}

export function KBToolResultCard({ name, result, sessionId, domainId }: KBToolResultCardProps) {
  const parsed = useMemo(() => parseKBResult(result), [result]);
  if (!parsed) return null;
  const docCount = parsed.documents?.length ?? 0;
  const nodeCount = parsed.sections?.length ?? parsed.nodes?.length ?? countStructureNodes(parsed.nodes_tree);
  const assets = collectAssetRefs(parsed);
  const meta = [
    docCount > 0 ? `${docCount} 文档` : '',
    nodeCount > 0 ? `${nodeCount} 节点` : '',
    assets.length > 0 ? `${assets.length} 资产` : '',
    parsed.no_kb_bound ? '未绑定 KB' : '',
  ].filter(Boolean).join(' · ');

  return (
    <div className="rounded-lg border border-[var(--border-color)] bg-[var(--bg-card)] p-3 text-sm">
      <div className="flex items-center gap-2 text-[var(--text-primary)]">
        <FileText className="h-4 w-4 text-[var(--accent-600)]" />
        <span className="font-medium">{name}</span>
        {meta && <span className="text-xs text-[var(--text-secondary)]">{meta}</span>}
      </div>
      {parsed.documents && parsed.documents.length > 0 && (
        <div className="mt-2 space-y-1">
          {parsed.documents.slice(0, 3).map((doc) => (
            <div key={doc.doc_id || doc.id || doc.title} className="min-w-0 rounded-md bg-[var(--bg-secondary)] px-2 py-1.5 text-xs">
              <div className="truncate font-medium text-[var(--text-primary)]">{doc.title || doc.doc_name || doc.doc_id || doc.id}</div>
              <div className="mt-0.5 truncate font-mono text-[10px] text-[var(--text-secondary)]">
                {[doc.doc_id || doc.id, doc.version, doc.page_count ? `${doc.page_count}p` : '', doc.line_count ? `${doc.line_count}l` : '', doc.node_count ? `${doc.node_count}n` : ''].filter(Boolean).join(' · ')}
              </div>
            </div>
          ))}
        </div>
      )}
      {assets.length > 0 && (
        <div className="mt-3 grid gap-2 sm:grid-cols-2">
          {assets.slice(0, 4).map((asset) => (
            <KBAssetLink
              key={`${asset.asset_uri || asset.uri}-${asset.content_hash}`}
              asset={asset}
              sessionId={sessionId}
              domainId={domainId}
            />
          ))}
        </div>
      )}
    </div>
  );
}

type KBToolParsedResult = KBSectionTextResult & {
  documents?: Array<{
    doc_id?: string;
    id?: string;
    doc_name?: string;
    title?: string;
    version?: string;
    page_count?: number;
    line_count?: number;
    node_count?: number;
  }>;
  no_kb_bound?: boolean;
  nodes_tree?: Array<{ children?: unknown[] }>;
};

function KBAssetLink({ asset, sessionId, domainId }: { asset: KBAssetRef; sessionId?: string; domainId?: string }) {
  const [url, setURL] = useState(asset.signed_url || '');
  const [failed, setFailed] = useState(false);
  const uri = asset.asset_uri || asset.uri || '';

  useEffect(() => {
    if (url || !uri) return;
    let cancelled = false;
    void assetClient.resolveAsset(uri, {
      purpose: 'kb_section_text',
      sessionId,
      domainId,
    })
      .then((res) => {
        if (!cancelled) setURL(res.url);
      })
      .catch(() => {
        if (!cancelled) setFailed(true);
      });
    return () => {
      cancelled = true;
    };
  }, [domainId, sessionId, uri, url]);

  const label = asset.alt_text || asset.caption || asset.filename || uri;
  return (
    <a
      href={url || undefined}
      target="_blank"
      rel="noreferrer"
      className="flex items-center gap-2 rounded-md border border-[var(--border-color)] px-2 py-2 text-xs text-[var(--text-secondary)] hover:border-[var(--accent-border)] hover:text-[var(--accent-600)]"
      aria-disabled={!url}
      onClick={(e) => {
        if (!url) e.preventDefault();
      }}
    >
      <ImageIcon className="h-4 w-4 shrink-0" />
      <span className="min-w-0 flex-1 truncate">{failed ? '资产暂不可访问' : label}</span>
      {url && <ExternalLink className="h-3.5 w-3.5 shrink-0" />}
    </a>
  );
}

function parseKBResult(result?: string): KBToolParsedResult | null {
  if (!result) return null;
  try {
    const parsed = JSON.parse(result) as KBToolParsedResult;
    if (Array.isArray(parsed.sections) || Array.isArray(parsed.asset_refs) || Array.isArray(parsed.documents) || parsed.no_kb_bound) {
      return parsed;
    }
    if (Array.isArray(parsed.nodes)) {
      if (parsed.nodes.some((node) => Array.isArray((node as { asset_refs?: unknown }).asset_refs))) {
        return parsed;
      }
      return { ...parsed, nodes_tree: parsed.nodes as KBToolParsedResult['nodes_tree'], nodes: undefined };
    }
    return null;
  } catch {
    return null;
  }
}

function collectAssetRefs(result: KBToolParsedResult): KBAssetRef[] {
  const refs: KBAssetRef[] = [];
  if (Array.isArray(result.asset_refs)) refs.push(...result.asset_refs);
  for (const node of result.nodes || []) {
    if (Array.isArray(node.asset_refs)) refs.push(...node.asset_refs);
  }
  const seen = new Set<string>();
  return refs.filter((ref) => {
    const uri = ref.asset_uri || ref.uri || '';
    if (!uri || seen.has(uri)) return false;
    seen.add(uri);
    return true;
  });
}

function countStructureNodes(nodes?: Array<{ children?: unknown[] }>): number {
  if (!nodes) return 0;
  let count = 0;
  const visit = (items: unknown[]) => {
    for (const item of items) {
      count++;
      const children = (item as { children?: unknown[] }).children;
      if (Array.isArray(children)) visit(children);
    }
  };
  visit(nodes);
  return count;
}
