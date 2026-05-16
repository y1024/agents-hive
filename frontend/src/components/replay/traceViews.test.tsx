import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { SessionTraceResponse, TraceTimelineItem } from '../../types/api';
import { AgentTreeView } from './AgentTreeView';
import { EventDetailPanel } from './EventDetailPanel';
import { ReplayTimeline } from './ReplayTimeline';
import { traceItemKey } from './traceItemKey';

describe('Replay trace views', () => {
  const trace: SessionTraceResponse = {
    session_id: 'sess-1',
    trace_id: 'trace-root',
    items: [
      {
        kind: 'span',
        trace_id: 'trace-root',
        span_id: 'span-1',
        operation: 'llm.call',
        service: 'master',
        status: 'ok',
        duration_ms: 42,
        timestamp: '2026-05-06T10:00:00Z',
      },
      {
        kind: 'quality_event',
        trace_id: 'trace-root',
        span_id: 'span-2',
        operation: 'quality.reflection',
        status: 'warn',
        attributes: {
          quality_event: {
            name: 'quality.reflection',
            turn_id: 'turn-1',
            domain_id: 'quality_analysis',
            source_kind: 'master',
            source_name: 'react',
            owner_scope: 'user',
            owner_id: 'user-1',
            final_status: 'blocked',
            reflection: {
              trigger: 'batch_loop',
              severity: 'warn',
              tool_name: 'bash',
              consecutive: 3,
              summary: '连续失败后注入反思提示',
              recommended: ['换一个验证命令'],
              injected: true,
            },
          },
        },
        timestamp: '2026-05-06T10:00:01Z',
      },
    ],
    agent_tree: [
      {
        trace_id: 'trace-root',
        agent_id: 'master',
        type: 'master',
        children: [
          {
            trace_id: 'trace-child',
            agent_id: 'worker-1',
            type: 'worker',
            status: 'pass',
          },
        ],
      },
    ],
  };

  it('renders trace timeline items and reports selected trace index', () => {
    const onSelect = vi.fn();
    render(<ReplayTimeline trace={trace} selectedTraceIndex={1} onSelectTrace={onSelect} />);

    expect(screen.getByText('Trace')).toBeInTheDocument();
    expect(screen.getByText('llm.call')).toBeInTheDocument();
    expect(screen.getByText('反思')).toBeInTheDocument();

    fireEvent.click(screen.getByText('llm.call'));

    expect(onSelect).toHaveBeenCalledWith(0);
  });

  it('renders backend agent tree nodes', () => {
    render(<AgentTreeView nodes={trace.agent_tree} />);

    expect(screen.getByText('master')).toBeInTheDocument();
    expect(screen.getByText('worker-1')).toBeInTheDocument();
  });

  it('renders reflection quality payload in the detail panel', () => {
    render(<EventDetailPanel event={null} traceItem={trace.items[1]} />);

    expect(screen.getByText('Trace 事件')).toBeInTheDocument();
    expect(screen.getByText('质量反思')).toBeInTheDocument();
    expect(screen.getByText('batch_loop')).toBeInTheDocument();
    expect(screen.getByText('连续失败后注入反思提示')).toBeInTheDocument();
  });

  it('renders quality domain and source badges in the detail panel', () => {
    const item: TraceTimelineItem = {
      kind: 'quality_event',
      trace_id: 'trace-root',
      span_id: 'span-3',
      operation: 'quality.context_build',
      status: 'ok',
      attributes: {
        quality_event: {
          name: 'quality.context_build',
          domain_id: 'customer_service',
          source_kind: 'master',
          source_name: 'react',
          owner_scope: 'user',
          owner_id: 'user-1',
          failure_type: 'none',
          final_status: 'pass',
        },
      },
      timestamp: '2026-05-06T10:00:02Z',
    };

    render(<EventDetailPanel event={null} traceItem={item} />);

    expect(screen.getByText('domain: customer_service')).toBeInTheDocument();
    expect(screen.getByText('source: master/react')).toBeInTheDocument();
    expect(screen.getByText('owner: user:user-1')).toBeInTheDocument();
  });

  it('builds stable keys for quality timeline items from quality metadata', () => {
    const item: TraceTimelineItem = {
      kind: 'quality_event',
      trace_id: 'trace-root',
      span_id: 'span-fallback',
      operation: 'quality.tool_decision',
      attributes: {
        quality_event: {
          name: 'quality.tool_decision',
          turn_id: 'turn-42',
          span_id: 'span-quality',
          attributes: { tool_call_id: 'call-1' },
        },
      },
      timestamp: '2026-05-06T10:00:03Z',
    };

    expect(traceItemKey(item, 9)).toBe('quality:turn-42:quality.tool_decision:span-quality:call-1');
  });

  it('renders generic trace attributes when no quality event exists', () => {
    const item: TraceTimelineItem = {
      kind: 'span',
      trace_id: 'trace-root',
      span_id: 'span-1',
      operation: 'tool.exec',
      status: 'error',
      attributes: { error: 'failed' },
      timestamp: '2026-05-06T10:00:00Z',
    };

    render(<EventDetailPanel event={null} traceItem={item} />);

    expect(screen.getByText('tool.exec')).toBeInTheDocument();
    expect(screen.getByText(/"error": "failed"/)).toBeInTheDocument();
  });
});
