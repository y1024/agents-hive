import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { TodosList } from './TodosList';
import { useTodosStore, type TodoSnapshot } from '../../store/todos';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, options?: Record<string, unknown>) => {
      const map: Record<string, string> = {
        'todos.ariaLabel': 'Session todos',
        'todos.title': 'Todos',
        'todos.taskCount': `${options?.count ?? 0} tasks`,
        'todos.pausedHint': 'Send a message to continue',
        'todos.resume': 'Resume',
        'todos.resumeRunning': 'Resuming',
        'todos.collapse': 'Collapse todos',
        'todos.expand': 'Expand todos',
        'todos.emptyActivePlan': 'Plan is active; todos will appear here.',
        'todos.status.pending': 'Pending',
        'todos.status.in_progress': 'In progress',
        'todos.status.completed': 'Completed',
        'todos.status.cancelled': 'Cancelled',
        'todos.planStatus.executing': 'Executing',
        'todos.planStatus.paused': 'Paused',
      };
      return map[key] ?? key;
    },
  }),
}));

vi.mock('../../hooks/useNodeClient', () => ({
  useNodeClient: () => ({
    resumeTodos: vi.fn(),
  }),
}));

function snapshot(overrides: Partial<TodoSnapshot> = {}): TodoSnapshot {
  return {
    session_id: 's1',
    plan_status: 'executing',
    plan_version: 3,
    todos: [
      {
        id: 't1',
        session_id: 's1',
        content: 'Read context',
        status: 'in_progress',
        order: 0,
        version: 3,
        created_at: '2026-05-05T00:00:00Z',
        updated_at: '2026-05-05T00:00:00Z',
      },
    ],
    updated_at: '2026-05-05T00:00:00Z',
    ...overrides,
  };
}

describe('TodosList', () => {
  beforeEach(() => {
    useTodosStore.getState().clear();
  });

  it('announces plan status changes with aria-live', () => {
    useTodosStore.getState().applySnapshot(snapshot());

    render(<TodosList variant="desktop" />);

    const liveRegion = screen.getByText('Executing');
    expect(liveRegion).toHaveAttribute('aria-live', 'polite');
  });

  it('collapses desktop todos into a rail without unmounting the status announcement', () => {
    useTodosStore.getState().applySnapshot(snapshot());

    render(<TodosList variant="desktop" />);
    fireEvent.click(screen.getByRole('button', { name: 'Collapse todos' }));

    expect(screen.queryByText('Read context')).not.toBeInTheDocument();
    expect(screen.getByText('Todos')).toBeInTheDocument();
    expect(screen.getByText('Executing')).toHaveAttribute('aria-live', 'polite');
    expect(screen.getByRole('button', { name: 'Expand todos' })).toBeInTheDocument();
  });
});
