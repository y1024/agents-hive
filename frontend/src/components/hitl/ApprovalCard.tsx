import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Shield, HelpCircle, CheckSquare, List, Lock, Eye, Loader2 } from 'lucide-react';
import { useCanvasStore } from '../../store/canvas';
import { useAppStore } from '../../store/app';
import type { InputRequest, InputResponse } from '../../types/api';
import { formatDateTime } from '../../utils/date';
import { getToolDisplayName } from '../../utils/toolName';

// 根据工具类型提取关键操作字段
function formatPermissionData(toolName: string | undefined, data: Record<string, unknown>): string {
  const keyFields: Record<string, string[]> = {
    bash: ['command'],
    edit: ['file_path'],
    write_file: ['file_path'],
    read_file: ['file_path'],
    glob: ['pattern', 'path'],
    grep: ['pattern', 'path'],
    skill: ['name'],
  };

  const fields = keyFields[toolName || ''] || ['command', 'file_path', 'path'];

  for (const field of fields) {
    const val = data[field];
    if (val != null && val !== '') {
      return String(val);
    }
  }

  // 兜底：取第一个非空值
  for (const [, v] of Object.entries(data)) {
    if (v != null && v !== '') return String(v);
  }
  return '';
}

// 类型图标映射
const TYPE_ICONS: Record<string, typeof Shield> = {
  approval: Shield,
  clarification: HelpCircle,
  confirmation: CheckSquare,
  choice: List,
  permission: Lock,
};

interface Props {
  request: InputRequest;
  onSubmit: (resp: InputResponse) => void;
}

interface ChoiceView {
  prompt: string;
  options: string[];
}

function parsePromptOptions(prompt: string): ChoiceView | null {
  const lines = prompt.split(/\r?\n/);
  const markerIndex = lines.findIndex((line) => /^\s*(选项|options?)\s*[:：]\s*$/i.test(line.trim()));
  if (markerIndex < 0) return null;

  const optionLines = lines.slice(markerIndex + 1);
  const options: string[] = [];
  for (const rawLine of optionLines) {
    const line = rawLine.trim();
    if (!line) continue;
    const match = line.match(/^(?:[-*]\s*)?(?:\d+[).、]|[A-Za-z][).、])\s*(.+)$/);
    if (!match) {
      return null;
    }
    options.push(match[1].trim());
  }
  if (options.length === 0) return null;

  const promptText = lines.slice(0, markerIndex).join('\n').trim();
  return {
    prompt: promptText || prompt,
    options,
  };
}

function buildChoiceView(request: InputRequest): ChoiceView {
  if ((request.type === 'choice' || request.type === 'clarification') && request.options?.length) {
    return { prompt: request.prompt, options: request.options };
  }
  if (request.type === 'clarification') {
    const parsed = parsePromptOptions(request.prompt);
    if (parsed) return parsed;
  }
  return { prompt: request.prompt, options: [] };
}

export function ApprovalCard({ request, onSubmit }: Props) {
  const { t } = useTranslation();
  const [value, setValue] = useState(request.default || '');
  const [selectedOption, setSelectedOption] = useState<string>('');
  const choiceView = buildChoiceView(request);
  const hasChoices = choiceView.options.length > 0;

  // 提交响应
  const submit = (action: string, val?: string) => {
    onSubmit({
      request_id: request.id,
      task_id: request.task_id,
      value: val || value,
      action,
    });
  };

  // 类型标签映射
  const typeLabel: Record<string, string> = {
    approval: t('hitl.approvalRequired'),
    clarification: t('hitl.clarificationNeeded'),
    confirmation: t('hitl.confirmAction'),
    choice: t('hitl.selectOption'),
    permission: t('hitl.permissionRequest'),
  };

  const Icon = TYPE_ICONS[request.type] || Shield;
  const isPermission = request.type === 'permission';
  const isWenyanPublish = isPermission && request.tool_name === 'wenyan__publish_article';

  return (
    <div
      className="overflow-hidden"
      style={{
        border: `1.5px solid ${isPermission ? 'var(--card-error-border)' : 'var(--card-tool-border)'}`,
        borderRadius: 10,
        background: isPermission ? 'var(--card-error-bg)' : 'var(--card-tool-bg)',
      }}
    >
      {/* Header: 类型图标 + 标签 + 工具名 + 时间 */}
      <div className="flex items-center gap-2 px-4 py-3" style={{ minHeight: 44 }}>
        <Icon
          className="w-4 h-4 shrink-0"
          style={{ color: isPermission ? '#ef4444' : '#3b82f6' }}
        />
        <span className="text-[13px] font-semibold text-[var(--text-primary)]">
          {typeLabel[request.type] || request.type}
        </span>
        {request.tool_name && (
          <span className="px-1.5 py-0.5 text-xs font-mono rounded"
            style={{
              background: isPermission ? 'rgba(239,68,68,0.1)' : 'rgba(59,130,246,0.1)',
              color: isPermission ? '#ef4444' : '#3b82f6',
            }}
          >
            {getToolDisplayName(request.tool_name, t)}
          </span>
        )}
        <span className="text-xs text-[var(--text-secondary)] ml-auto shrink-0">
          {formatDateTime(request.created_at).split(' ')[1]}
        </span>
      </div>

      {/* 内容区 */}
      <div className="px-4 pb-4" style={{ borderTop: '1px solid var(--border-color)' }}>
        {/* 提示内容 */}
        <p className="text-sm text-[var(--text-primary)] mt-3 mb-3 whitespace-pre-wrap">{choiceView.prompt}</p>

        {/* wenyan 发布专用 UI */}
        {isWenyanPublish && (
          <WenyanPublishPreview request={request} onSubmit={onSubmit} />
        )}

        {/* 普通权限请求 - 操作详情（内容为空时不渲染） */}
        {isPermission && !isWenyanPublish && (() => {
          const detail = request.data
            ? formatPermissionData(request.tool_name, request.data as Record<string, unknown>)
            : '';
          return detail ? (
            <pre className="mb-3 p-3 rounded-lg text-xs font-mono break-all whitespace-pre-wrap text-[var(--text-secondary)]"
              style={{ background: 'var(--bg-primary)', border: '1px solid var(--border-color)' }}
            >
              {detail}
            </pre>
          ) : null;
        })()}

        {/* 普通权限请求的操作按钮 */}
        {isPermission && !isWenyanPublish && (
          <div className="flex items-center justify-end gap-2 mt-2">
            <button
              onClick={() => submit('reject')}
              className="px-3.5 py-1.5 text-sm font-medium rounded-lg transition-colors text-[var(--text-secondary)] hover:text-red-600 hover:bg-red-50 dark:hover:bg-red-900/20 dark:hover:text-red-400"
            >
              {t('hitl.reject')}
            </button>
            <button
              onClick={() => submit('approve')}
              className="px-3.5 py-1.5 text-sm font-medium bg-emerald-500 text-white rounded-lg hover:bg-emerald-600 transition-colors"
            >
              {t('hitl.approve')}
            </button>
          </div>
        )}

        {/* 选择型 */}
        {hasChoices && (
          <div className="mb-3 space-y-1.5">
            {choiceView.options.map((opt) => (
              <button
                key={opt}
                onClick={() => setSelectedOption(opt)}
                className={`w-full text-left px-3 py-2 rounded-lg text-sm transition-colors ${
                  selectedOption === opt
                    ? 'bg-[var(--accent-50)] dark:bg-[var(--accent-light)] text-[var(--accent-600)] dark:text-[var(--accent-300)] border border-[var(--accent-300)] dark:border-[var(--accent-700)]'
                    : 'bg-[var(--bg-primary)] text-[var(--text-primary)] border border-[var(--border-color)] hover:border-[var(--text-secondary)]'
                }`}
              >
                {opt}
              </button>
            ))}
          </div>
        )}

        {/* 文本输入（澄清型） */}
        {request.type === 'clarification' && !hasChoices && (
          <textarea
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder={t('hitl.responsePlaceholder')}
            rows={2}
            className="w-full mb-3 px-3 py-2.5 bg-[var(--bg-primary)] border border-[var(--border-color)] rounded-lg text-sm text-[var(--text-primary)] placeholder:text-[var(--text-secondary)] focus:border-[var(--accent)] focus:ring-2 focus:ring-[var(--accent-subtle)] focus:outline-none resize-none"
          />
        )}

        {/* 非 wenyan 的 approval/confirmation/choice/clarification 按钮 */}
        {!isWenyanPublish && (
          <div className="flex items-center justify-end gap-2">
            {(request.type === 'approval' || request.type === 'confirmation') && (
              <>
                <button
                  onClick={() => submit('reject')}
                  className="px-3.5 py-1.5 text-sm font-medium rounded-lg transition-colors text-[var(--text-secondary)] hover:text-red-600 hover:bg-red-50 dark:hover:bg-red-900/20 dark:hover:text-red-400"
                >
                  {t('hitl.reject')}
                </button>
                <button
                  onClick={() => submit('approve')}
                  className="px-3.5 py-1.5 text-sm font-medium bg-emerald-500 text-white rounded-lg hover:bg-emerald-600 transition-colors"
                >
                  {t('hitl.approve')}
                </button>
              </>
            )}
            {hasChoices && (
              <button
                onClick={() => submit('proceed', selectedOption)}
                disabled={!selectedOption}
                className="px-3.5 py-1.5 text-sm font-medium bg-[var(--accent-600)] text-white rounded-lg hover:bg-[var(--accent-700)] disabled:opacity-30 transition-all"
              >
                {t('hitl.confirm')}
              </button>
            )}
            {request.type === 'clarification' && !hasChoices && (
              <button
                onClick={() => submit('proceed')}
                disabled={!value.trim()}
                className="px-3.5 py-1.5 text-sm font-medium bg-[var(--accent-600)] text-white rounded-lg hover:bg-[var(--accent-700)] disabled:opacity-30 transition-all"
              >
                {t('hitl.submit')}
              </button>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

// ===== wenyan 公众号发布专用预览组件 =====

const WENYAN_THEMES = [
  { id: 'default', name: '默认' },
  { id: 'orangeheart', name: '橙心' },
  { id: 'rainbow', name: '彩虹' },
  { id: 'lapis', name: '天青石' },
  { id: 'pie', name: '派' },
  { id: 'maize', name: '玉米' },
  { id: 'purple', name: '紫调' },
  { id: 'phycat', name: '物理猫' },
];

function WenyanPublishPreview({ request, onSubmit }: { request: InputRequest; onSubmit: (r: InputResponse) => void }) {
  const { t } = useTranslation();

  const data = request.data as Record<string, unknown> | undefined;
  const originalContent = String(data?.content || '');
  const originalTheme = String(data?.theme_id || 'default');

  // 从 frontmatter 提取标题，兼容旧 Markdown 一级标题格式
  const extractTitle = (content: string): string => {
    const fmMatch = content.match(/^---\s*\n([\s\S]*?)\n---/);
    if (fmMatch) {
      const titleMatch = fmMatch[1].match(/^title:\s*(.+)/m);
      if (titleMatch) return titleMatch[1].trim();
    }
    const h1Match = content.match(/^#\s+(.+)/m);
    return h1Match ? h1Match[1].trim() : '';
  };

  const [themeId, setThemeId] = useState(originalTheme);
  const [title, setTitle] = useState(() => extractTitle(originalContent));
  const [previewing, setPreviewing] = useState(false);

  // 将标题注入 frontmatter（替换已有 title 或创建 frontmatter）
  const contentWithTitle = (content: string, newTitle: string): string => {
    if (!newTitle.trim()) return content;
    const fmRegex = /^(---\s*\n)([\s\S]*?)(\n---)/;
    const fmMatch = content.match(fmRegex);
    if (fmMatch) {
      const [, open, body, close] = fmMatch;
      const updated = /^title:\s*.+/m.test(body)
        ? body.replace(/^title:\s*.+/m, `title: ${newTitle.trim()}`)
        : `title: ${newTitle.trim()}\n${body}`;
      return content.replace(fmRegex, `${open}${updated}${close}`);
    }
    // 无 frontmatter 时创建，同时移除旧的 # 标题行（避免重复标题）
    const cleaned = content.replace(/^#\s+.+\n*/m, '');
    return `---\ntitle: ${newTitle.trim()}\n---\n\n${cleaned}`;
  };

  // 去掉 frontmatter，只保留正文给渲染器
  const stripFrontmatter = (content: string): string => {
    return content.replace(/^---\s*\n[\s\S]*?\n---\s*\n*/, '');
  };

  const handleApprove = () => {
    const override: Record<string, string> = {};
    if (themeId !== originalTheme) override.theme_id = themeId;
    const mergedContent = contentWithTitle(originalContent, title);
    if (mergedContent !== originalContent) override.content = mergedContent;
    onSubmit({
      request_id: request.id,
      task_id: request.task_id,
      value: Object.keys(override).length > 0 ? JSON.stringify(override) : '',
      action: 'approve',
    });
  };

  const handleReject = () => {
    onSubmit({ request_id: request.id, task_id: request.task_id, value: '', action: 'reject' });
  };

  return (
    <div className="space-y-3">
      {/* 标题输入 */}
      <div className="flex items-center gap-2">
        <label className="text-xs text-[var(--text-secondary)] shrink-0">标题</label>
        <input
          type="text"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="文章标题（必填）"
          className="flex-1 px-2 py-1.5 text-xs bg-[var(--bg-primary)] border border-[var(--border-color)] rounded-md text-[var(--text-primary)] placeholder:text-[var(--text-secondary)] focus:outline-none focus:border-[var(--accent)]"
        />
      </div>
      {/* 主题选择器 */}
      <div className="flex items-center gap-2">
        <label className="text-xs text-[var(--text-secondary)] shrink-0">主题</label>
        <select
          value={themeId}
          onChange={(e) => setThemeId(e.target.value)}
          className="flex-1 px-2 py-1.5 text-xs bg-[var(--bg-primary)] border border-[var(--border-color)] rounded-md text-[var(--text-primary)] focus:outline-none focus:border-[var(--accent)]"
        >
          {WENYAN_THEMES.map((theme) => (
            <option key={theme.id} value={theme.id}>{theme.name}</option>
          ))}
        </select>
      </div>

      {/* 预览按钮 → 调用 wenyan MCP 预览工具，在右侧 Canvas 面板中打开 HTML */}
      <div className="flex items-center gap-2">
        <button
          onClick={async () => {
            const mergedContent = contentWithTitle(originalContent, title);
            setPreviewing(true);
            try {
              const rawHtml = await useAppStore.getState().nodeClient.invokeTool(
                'wenyan__preview_article',
                { content: mergedContent, theme_id: themeId },
              );
              // wenyan 返回的是局部 HTML，需要套上完整的骨架，以便在 iframe 中正确预览
              const html = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no">
<style>
  body { margin: 0; padding: 20px; background: #fff; color: #333; }
  /* 给公众号容器限制最大宽度，模拟手机预览 */
  #preview-container {
    max-width: 600px;
    margin: 0 auto;
    background: #fff;
    padding: 15px;
    box-shadow: 0 4px 12px rgba(0,0,0,0.08);
    border-radius: 8px;
    min-height: 100vh;
  }
</style>
</head>
<body>
  <div id="preview-container">
    ${rawHtml}
  </div>
</body>
</html>`;
              useCanvasStore.getState().openArtifact({
                id: `wenyan-preview-${request.id}`,
                title: title.trim() || '文章预览',
                language: 'html',
                content: html,
                type: 'html',
              });
            } catch {
              // MCP 预览失败时 fallback 到 Markdown 渲染
              const fallback = title.trim()
                ? `# ${title.trim()}\n\n${stripFrontmatter(mergedContent)}`
                : stripFrontmatter(mergedContent);
              useCanvasStore.getState().openArtifact({
                id: `wenyan-preview-${request.id}`,
                title: title.trim() || '文章预览',
                language: 'markdown',
                content: fallback,
                type: 'markdown',
              });
            } finally {
              setPreviewing(false);
            }
          }}
          disabled={!originalContent || previewing}
          className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium rounded-lg transition-colors bg-[var(--accent-50)] text-[var(--accent-600)] hover:bg-[var(--accent-100)] dark:bg-[var(--accent-light)] dark:text-[var(--accent-300)] dark:hover:bg-[var(--accent-light)] disabled:opacity-40"
        >
          {previewing ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Eye className="w-3.5 h-3.5" />}
          {previewing ? '渲染中...' : '预览效果'}
        </button>
      </div>

      {/* 批准/拒绝 */}
      <div className="flex items-center justify-end gap-2 pt-1">
        <button
          onClick={handleReject}
          className="px-3.5 py-1.5 text-sm font-medium rounded-lg transition-colors text-[var(--text-secondary)] hover:text-red-600 hover:bg-red-50 dark:hover:bg-red-900/20 dark:hover:text-red-400"
        >
          {t('hitl.reject')}
        </button>
        <button
          onClick={handleApprove}
          disabled={!title.trim()}
          className="px-3.5 py-1.5 text-sm font-medium bg-emerald-500 text-white rounded-lg hover:bg-emerald-600 disabled:opacity-40 transition-colors"
        >
          {t('hitl.approve')}
        </button>
      </div>
    </div>
  );
}
