import { describe, it, expect, vi, beforeEach } from 'vitest';
import { waitFor, render, screen } from '@testing-library/react';
import { ToolAdapter } from '../ToolAdapter';

type ToolCallStatus = { status: 'running' | 'success' | 'error'; duration?: number };

const storeState = vi.hoisted(() => ({
  toolCallStatuses: {} as Record<string, ToolCallStatus>,
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, options?: Record<string, unknown>) => {
      const map: Record<string, string> = {
        'tools.invoked': 'Invoked',
        'tools.clickToExpand': 'Click to expand',
        'tools.clickToCollapse': 'Click to collapse',
        'tools.output': 'Output',
        'tools.input': 'Input',
        'todos.inlineUpdated': 'Todos updated',
        'todos.title': 'Todos',
        'todos.taskCount': `${options?.count ?? 0} tasks`,
        'todos.status.pending': 'Pending',
        'todos.status.in_progress': 'In progress',
        'todos.status.completed': 'Completed',
        'todos.status.cancelled': 'Cancelled',
        'todos.planStatus.planning': 'Planning',
      };
      return map[key] ?? key;
    },
  }),
}));

vi.mock('../../../store/chat', () => ({
  useChatStore: (selector: (s: unknown) => unknown) => selector(storeState),
}));

vi.mock('../../../utils/toolName', () => ({
  getToolDisplayName: (name: string) => (name === 'bash_exec' ? 'Shell' : name),
}));

const resolveAssetMock = vi.hoisted(() => vi.fn());

vi.mock('../../../api/node-client', () => ({
  LocalNodeClient: vi.fn().mockImplementation(() => ({
    resolveAsset: resolveAssetMock,
  })),
}));

const baseProps = {
  id: 'tc-1',
  name: 'bash_exec',
  args: '{"command":"ls"}',
  result: 'file1\nfile2',
};

describe('ToolAdapter status mapping', () => {
  beforeEach(() => {
    storeState.toolCallStatuses = {};
    resolveAssetMock.mockReset();
  });

  // 三状态 × live / replay 两 mode = 6 用例
  // live = store.toolCallStatuses[id] 有值（websocket 实时推送已入 store）
  // replay = store 无该 id 条目（session 恢复后尚未回放 / 历史只读）

  it('live / running: Running 徽标 + 运行态 chip（role=status）', () => {
    storeState.toolCallStatuses = { 'tc-1': { status: 'running' } };
    render(<ToolAdapter {...baseProps} hasError={false} />);
    expect(screen.getByText('Running')).toBeTruthy();
    expect(screen.getByRole('status')).toBeTruthy();
  });

  it('live / success: Completed 徽标 + 完成态 block toggle', () => {
    storeState.toolCallStatuses = { 'tc-1': { status: 'success' } };
    render(<ToolAdapter {...baseProps} hasError={false} />);
    expect(screen.getByText('Completed')).toBeTruthy();
    const buttons = screen.getAllByRole('button');
    // 至少有一个非 disabled 的 toggle（ToolExecutionBlock 的折叠按钮）
    expect(buttons.some((b) => !b.hasAttribute('disabled'))).toBe(true);
  });

  it('live / error: Error 徽标 + Tool 外壳 defaultOpen=true', () => {
    storeState.toolCallStatuses = { 'tc-1': { status: 'running' } };
    render(<ToolAdapter {...baseProps} hasError={true} />);
    expect(screen.getByText('Error')).toBeTruthy();
    // Collapsible 根节点 data-state 应为 open（error 时默认展开）
    const root = document.querySelector('[data-slot="collapsible"]');
    expect(root?.getAttribute('data-state')).toBe('open');
  });

  it('replay / running: store 恢复 running 态时徽标正确', () => {
    // replay 恢复：store 被 session history reducer 填回 running
    storeState.toolCallStatuses = { 'tc-1': { status: 'running' } };
    render(<ToolAdapter {...baseProps} hasError={false} />);
    expect(screen.getByText('Running')).toBeTruthy();
  });

  it('replay / success: store 无该 id 时回退到 Completed', () => {
    storeState.toolCallStatuses = {}; // replay 未回放任何 live 状态
    render(<ToolAdapter {...baseProps} hasError={false} />);
    expect(screen.getByText('Completed')).toBeTruthy();
  });

  it('replay / error: store 无 live 状态 + hasError=true 仍报 Error 并展开', () => {
    storeState.toolCallStatuses = {};
    render(<ToolAdapter {...baseProps} hasError={true} />);
    expect(screen.getByText('Error')).toBeTruthy();
    const root = document.querySelector('[data-slot="collapsible"]');
    expect(root?.getAttribute('data-state')).toBe('open');
  });

  it('todo_write / success: 渲染业务待办列表而不是通用 Completed 工具状态', () => {
    storeState.toolCallStatuses = { 'tc-todo': { status: 'success' } };
    const result = JSON.stringify({
      session_id: 's-1',
      plan_status: 'planning',
      plan_version: 2,
      todos: [
        {
          id: 't1',
          content: 'Locate and read README',
          status: 'pending',
          order: 0,
          version: 2,
          created_at: '2026-05-04T03:37:49.910202Z',
          updated_at: '2026-05-04T03:37:49.910202Z',
        },
        {
          id: 't2',
          content: 'Enumerate docs directory',
          status: 'pending',
          order: 1,
          version: 2,
          created_at: '2026-05-04T03:37:49.910202Z',
          updated_at: '2026-05-04T03:37:49.910202Z',
        },
      ],
      updated_at: '2026-05-04T03:37:49.910202Z',
    });

    const { container } = render(
      <ToolAdapter
        id="tc-todo"
        name="todo_write"
        args='{"expected_plan_version":1}'
        result={result}
        hasError={false}
      />,
    );

    expect(screen.getByText('Todos updated')).toBeTruthy();
    expect(screen.getByText(/Planning/)).toBeTruthy();
    expect(screen.getByText('2 tasks')).toBeTruthy();
    expect(screen.getByText('Locate and read README')).toBeTruthy();
    expect(screen.getByText('Enumerate docs directory')).toBeTruthy();
    expect(screen.getAllByText('Pending').length).toBe(2);
    expect(screen.queryByText('Output')).toBeNull();
    expect(screen.queryByText('Completed')).toBeNull();
    const list = container.querySelector('[data-testid="inline-todos-list"]');
    expect(list).toBeTruthy();
    expect(list?.className).not.toContain('rounded-xl');
    expect(list?.className).not.toContain('border');
  });

  it('kb tool result resolves asset refs with runtime KB context', async () => {
    storeState.toolCallStatuses = { 'tc-kb': { status: 'success' } };
    resolveAssetMock.mockResolvedValue({
      url: '/api/v1/assets/proxy?x=1',
      expires_in: 300,
      mime_type: 'image/png',
      size: 3,
    });
    const result = JSON.stringify({
      sections: [{ node_id: '0001', title: 'Policy', text: 'text' }],
      asset_refs: [{
        asset_uri: 'asset://kb/user/u1/ns/doc/hash.png',
        mime_type: 'image/png',
        content_hash: 'hash',
      }],
    });

    render(
      <ToolAdapter
        id="tc-kb"
        name="kb.section.text"
        args='{"doc_id":"doc","node_ids":["0001"]}'
        result={result}
        hasError={false}
        sessionId="sess-1"
        kbDomainId="support"
      />,
    );

    await waitFor(() => expect(resolveAssetMock).toHaveBeenCalledWith(
      'asset://kb/user/u1/ns/doc/hash.png',
      {
        purpose: 'kb_section_text',
        sessionId: 'sess-1',
        domainId: 'support',
      },
    ));
  });
});
