export type JournalEventType = 'tool_call' | 'file_change' | 'decision';

export interface JournalEvent {
  type: JournalEventType;
  timestamp: string;
  // tool_call 字段
  tool_name?: string;
  arguments?: string;
  result?: string;
  is_error?: boolean;
  duration_ms?: number;
  // file_change 字段
  file_path?: string;
  action?: 'create' | 'edit' | 'delete';
  summary?: string;
  // decision 字段
  decision?: string;
  reason?: string;
  quality_event?: QualityEventView;
}

export interface QualityEventView {
  name: string;
  case_id?: string;
  run_id?: string;
  trace_id?: string;
  span_id?: string;
  turn_id?: string;
  domain_id?: string;
  source_kind?: string;
  source_name?: string;
  owner_scope?: string;
  owner_id?: string;
  user_id?: string;
  route?: string;
  failure_type?: string;
  retry_reason?: string;
  final_status?: string;
  tool_decision?: {
    expected?: string[];
    actual?: string;
    decision?: string;
    args_hash?: string;
  };
  prompt?: {
    key?: string;
    version?: string;
    source?: string;
    language?: string;
  };
  context_build?: {
    message_count?: number;
    compressed?: boolean;
    memory_injected?: boolean;
    memory_ids?: number[];
    skipped_memory_ids?: number[];
    skipped_expired?: number;
    skipped_low_trust?: number;
    skipped_cross_user?: number;
    skipped_token_budget?: number;
    skipped_memory_total?: number;
    memory_domain_id?: string;
    memory_source_kind?: string;
    memory_source_name?: string;
    memory_owner_scope?: string;
    memory_owner_id?: string;
    attachment_count?: number;
    prompt_versions?: string[];
    estimated_tokens?: number;
    contamination_check?: string;
  };
  delegation?: {
    parent_trace_id?: string;
    child_trace_id?: string;
    agent_id?: string;
    agent_type?: string;
    group_id?: string;
    spawn_depth?: number;
    max_turns?: number;
    tool_whitelist?: string[];
    stop_reason?: string;
  };
}

export interface JournalResponse {
  session_id: string;
  events: JournalEvent[];
}

export interface JournalStats {
  tool_call_count: number;
  file_change_count: number;
  decision_count: number;
  started_at: string;
  ended_at?: string;
  has_error: boolean;
  quality_error_count?: number;
  dangerous_count?: number;
  delegation_count?: number;
  acp_count?: number;
  context_pollution_count?: number;
}

export interface JournalStatsResponse {
  stats: Record<string, JournalStats | null>;
}

export type CharacterState = 'idle' | 'thinking' | 'reading' | 'coding' | 'running' | 'success' | 'error';

export function getCharacterState(event: JournalEvent): CharacterState {
  if (event.type === 'decision') return 'thinking';
  if (event.is_error) return 'error';
  if (event.type === 'file_change') return 'coding';
  if (event.type !== 'tool_call') return 'idle';

  const tool = event.tool_name || '';
  const filesystemAction = filesystemEventAction(event);
  if (tool === 'filesystem' && filesystemAction) {
    if (['list', 'glob', 'grep', 'read'].includes(filesystemAction)) return 'reading';
    if (['write', 'edit', 'multiedit'].includes(filesystemAction)) return 'coding';
  }

  const readTools = [
    'read_file', 'glob', 'grep', 'ls',
    'web_search', 'web_fetch',
    'lsp_hover', 'lsp_definition', 'lsp_references',
    'lsp_symbols', 'lsp_diagnostics', 'lsp_completion',
    'memory',
  ];
  if (readTools.includes(tool)) return 'reading';

  const writeTools = [
    'write_file', 'edit', 'multi_edit', 'multiedit', 'apply_patch',
    'create_tool', 'remove_tool',
    'lsp_rename', 'lsp_format', 'lsp_actions',
  ];
  if (writeTools.includes(tool)) return 'coding';

  const runTools = [
    'bash',
    'spawn_agent', 'parallel_dispatch',
    'send_im_message',
    'skill', 'task', 'question',
    'batch',
  ];
  if (runTools.includes(tool)) return 'running';

  return 'running';
}

export function journalToolDisplayName(event: JournalEvent): string {
  const tool = event.tool_name || '';
  if (tool === 'filesystem') {
    const action = filesystemEventAction(event);
    if (action) return `${tool}.${action}`;
  }
  return tool;
}

function filesystemEventAction(event: JournalEvent): string {
  if (event.tool_name !== 'filesystem' || !event.arguments) return '';

  try {
    const parsed = JSON.parse(event.arguments) as unknown;
    if (!parsed || typeof parsed !== 'object') return '';
    const action = (parsed as { action?: unknown }).action;
    return typeof action === 'string' ? action.trim().toLowerCase() : '';
  } catch {
    return '';
  }
}

export function attachQualityEvent(event: JournalEvent): JournalEvent {
  if (event.type !== 'decision' || !event.reason) return event;

  try {
    const parsed = JSON.parse(event.reason) as unknown;
    if (!isQualityEventView(parsed)) return event;
    return { ...event, quality_event: parsed };
  } catch {
    return event;
  }
}

function isQualityEventView(value: unknown): value is QualityEventView {
  if (!value || typeof value !== 'object') return false;
  const name = (value as { name?: unknown }).name;
  return typeof name === 'string' && name.startsWith('quality.');
}
