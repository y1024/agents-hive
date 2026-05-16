import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ApprovalCard } from './ApprovalCard';
import type { InputRequest } from '../../types/api';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => {
      const map: Record<string, string> = {
        'hitl.clarificationNeeded': '需要澄清',
        'hitl.approvalRequired': '需要审批',
        'hitl.selectOption': '选择选项',
        'hitl.confirm': '确认',
        'hitl.submit': '提交',
        'hitl.approve': '批准',
        'hitl.reject': '拒绝',
        'hitl.responsePlaceholder': '输入你的回复...',
      };
      return map[key] ?? key;
    },
  }),
}));

vi.mock('../../store/canvas', () => ({
  useCanvasStore: () => ({ open: vi.fn() }),
}));

vi.mock('../../store/app', () => ({
  useAppStore: Object.assign(() => ({ client: null }), {
    getState: () => ({ language: 'zh' }),
  }),
}));

const baseRequest: InputRequest = {
  id: 'input-1',
  task_id: 'session-1',
  type: 'clarification',
  prompt: '请选择',
  created_at: '2026-05-14T12:00:00Z',
};

describe('ApprovalCard choices', () => {
  it('renders clarification with structured options as a choice control', () => {
    const onSubmit = vi.fn();
    render(
      <ApprovalCard
        request={{ ...baseRequest, options: ['允许全局安装', '允许项目内安装'] }}
        onSubmit={onSubmit}
      />,
    );

    expect(screen.queryByPlaceholderText('输入你的回复...')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: '允许项目内安装' }));
    fireEvent.click(screen.getByRole('button', { name: '确认' }));

    expect(onSubmit).toHaveBeenCalledWith({
      request_id: 'input-1',
      task_id: 'session-1',
      value: '允许项目内安装',
      action: 'proceed',
    });
  });

  it('parses numbered options embedded in a legacy clarification prompt', () => {
    render(
      <ApprovalCard
        request={{
          ...baseRequest,
          prompt: '可以执行，但需要先安装 browser_interact 依赖的 agent-browser。\n\n选项:\n1) 允许全局安装 npm install -g agent-browser\n2) 允许项目内安装\n3) 不允许安装',
        }}
        onSubmit={vi.fn()}
      />,
    );

    expect(screen.getByText('可以执行，但需要先安装 browser_interact 依赖的 agent-browser。')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '允许全局安装 npm install -g agent-browser' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '允许项目内安装' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '不允许安装' })).toBeInTheDocument();
    expect(screen.queryByPlaceholderText('输入你的回复...')).toBeNull();
  });

  it('does not render approval options as a choice control', () => {
    const onSubmit = vi.fn();
    render(
      <ApprovalCard
        request={{
          ...baseRequest,
          type: 'approval',
          prompt: '允许执行工具？',
          options: ['approve', 'reject'],
        }}
        onSubmit={onSubmit}
      />,
    );

    expect(screen.queryByRole('button', { name: 'approve' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'reject' })).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: '批准' }));
    expect(onSubmit).toHaveBeenCalledWith({
      request_id: 'input-1',
      task_id: 'session-1',
      value: '',
      action: 'approve',
    });
  });
});
