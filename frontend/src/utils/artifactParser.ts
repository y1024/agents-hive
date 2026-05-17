export interface ContentSegment {
  type: 'text' | 'artifact';
  content: string;
  assetUri?: string;
  // artifact 专属
  artifactType?: 'markdown' | 'html' | 'code' | 'ppt';
  language?: string;
  title?: string;
  isLoading?: boolean;
}

// 从属性字符串中提取 type / title / language（顺序无关）
// 支持单引号和双引号，避免 subtitle= / data-type= 等误匹配
function parseAttrs(attrStr: string): { type?: string; title?: string; language?: string } {
  const get = (key: string) =>
    attrStr.match(new RegExp(`(?:^|\\s)${key}=["']([^"']*)["']`))?.[1];
  return { type: get('type'), title: get('title'), language: get('language') };
}

function toArtifactType(raw: string | undefined): ContentSegment['artifactType'] {
  return (['markdown', 'html', 'code', 'ppt'].includes(raw ?? '')
    ? raw
    : 'markdown') as ContentSegment['artifactType'];
}

// 解析已闭合的 artifact 标签，追加到 segments，返回新的 lastIndex
function parseClosedArtifacts(
  raw: string,
  segments: ContentSegment[],
  startIndex: number,
): number {
  const re = /<artifact\b([^>]*)>([\s\S]*?)<\/artifact>/g;
  re.lastIndex = startIndex;
  let lastIndex = startIndex;
  let match: RegExpExecArray | null;
  while ((match = re.exec(raw)) !== null) {
    if (match.index > lastIndex) {
      const text = raw.slice(lastIndex, match.index).replace(/^\n+|\n+$/g, '');
      if (text) segments.push({ type: 'text', content: text });
    }
    const attrs = parseAttrs(match[1]);
    segments.push({
      type: 'artifact',
      content: match[2].replace(/^\n+|\n+$/g, ''),
      artifactType: toArtifactType(attrs.type),
      language: attrs.language,
      title: attrs.title || '文档',
      isLoading: false,
    });
    lastIndex = match.index + match[0].length;
  }
  return lastIndex;
}

// 解析混合内容，返回有序片段数组
export function parseMessageContent(raw: string): ContentSegment[] {
  const segments: ContentSegment[] = [];
  const lastIndex = parseClosedArtifacts(raw, segments, 0);
  const tail = raw.slice(lastIndex).replace(/^\n+|\n+$/g, '');
  if (tail) segments.push({ type: 'text', content: tail });
  return segments;
}

export interface ArtifactManifest {
  uri: string;
  title: string;
  type: 'markdown' | 'html' | 'code' | 'ppt';
  language?: string;
}

export function mergeArtifactManifestSegments(
  segments: ContentSegment[],
  artifacts?: ArtifactManifest[],
): ContentSegment[] {
  if (!artifacts?.length) return segments;
  let artifactIndex = 0;
  return segments.map((seg) => {
    if (seg.type !== 'artifact') return seg;
    const manifest = artifacts[artifactIndex++];
    if (!manifest) return seg;
    return {
      ...seg,
      assetUri: manifest.uri,
      title: manifest.title || seg.title,
      artifactType: manifest.type || seg.artifactType,
      language: manifest.language || seg.language,
    };
  });
}

// 流式传输中：检测是否有未闭合的 <artifact> 标签
export function hasOpenArtifact(raw: string): boolean {
  const openCount = (raw.match(/<artifact\b/g) || []).length;
  const closeCount = (raw.match(/<\/artifact>/g) || []).length;
  return openCount > closeCount;
}

// 流式中有未闭合标签时：解析已闭合部分 + 生成骨架段
export function parseMessageContentWithSkeleton(raw: string): ContentSegment[] {
  const segments: ContentSegment[] = [];
  const lastIndex = parseClosedArtifacts(raw, segments, 0);

  // 检测未闭合的完整开标签（要求 > 存在，避免残片触发假骨架）
  const remaining = raw.slice(lastIndex);
  const openMatch = remaining.match(/<artifact\b([^>]*)>/);
  if (openMatch) {
    const beforeOpen = remaining.slice(0, openMatch.index!).replace(/^\n+|\n+$/g, '');
    if (beforeOpen) segments.push({ type: 'text', content: beforeOpen });
    const attrs = parseAttrs(openMatch[1]);
    segments.push({
      type: 'artifact',
      content: '',
      artifactType: toArtifactType(attrs.type),
      language: attrs.language,
      title: attrs.title || '文档',
      isLoading: true,
    });
  } else {
    const tail = remaining.replace(/^\n+|\n+$/g, '');
    if (tail) segments.push({ type: 'text', content: tail });
  }
  return segments;
}
