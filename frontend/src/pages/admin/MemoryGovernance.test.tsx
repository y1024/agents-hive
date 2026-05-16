import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryGovernance } from './MemoryGovernance';

const mockAddToast = vi.fn();
const mockClient = {
  adminGetMemoryGovernance: vi.fn(),
  adminGetEmbeddingBacklogStats: vi.fn(),
  adminGetMemoryInjectionExplain: vi.fn(),
  adminGetMemoryProductionMetrics: vi.fn(),
  adminListMemoryPromotionCandidates: vi.fn(),
  adminListOptimizationApprovals: vi.fn(),
  adminPruneMemoryGovernance: vi.fn(),
  adminExportMemory: vi.fn(),
  adminImportMemory: vi.fn(),
  adminPlanVectorSpaceMigration: vi.fn(),
};

vi.mock('../../hooks/useNodeClient', () => ({
  useNodeClient: () => mockClient,
}));

vi.mock('../../store/toast', () => ({
  useToastStore: (selector: (state: { addToast: typeof mockAddToast }) => unknown) => selector({ addToast: mockAddToast }),
}));

describe('MemoryGovernance', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockClient.adminGetMemoryGovernance.mockResolvedValue({
      total: 0,
      missing_governance: 0,
      expired: 0,
      low_confidence: 0,
      cross_user_risk: 0,
      policy: { min_confidence: 0.5, max_memories: 0 },
    });
    mockClient.adminGetEmbeddingBacklogStats.mockResolvedValue({ total: 0, by_state: null });
    mockClient.adminGetMemoryInjectionExplain.mockResolvedValue({ items: null, total: 0, limit: 10, source: 'fallback_empty' });
    mockClient.adminGetMemoryProductionMetrics.mockResolvedValue({
      source: 'fallback_empty',
      window_minutes: 1440,
      snapshot: null,
      series: null,
      alerts: null,
    });
    mockClient.adminListMemoryPromotionCandidates.mockResolvedValue({ items: null, total: 0, limit: 20 });
    mockClient.adminListOptimizationApprovals.mockResolvedValue({ items: null });
    mockClient.adminPruneMemoryGovernance.mockResolvedValue({
      dry_run: true,
      matched: 0,
      deleted: 0,
      delete_ids: null,
      reasons: null,
    });
    mockClient.adminExportMemory.mockResolvedValue({ version: 1, memories: null });
    mockClient.adminImportMemory.mockResolvedValue({ imported: 0, ids: [] });
    mockClient.adminPlanVectorSpaceMigration.mockResolvedValue({
      plan: { dry_run: true, scanned: 0, updates: null },
      applied: false,
      updated: 0,
    });
  });

  it('renders null collection responses without crashing', async () => {
    render(<MemoryGovernance />);

    await waitFor(() => {
      expect(screen.getByText('Memory 生产治理')).toBeInTheDocument();
    });

    expect(screen.getByText('暂无生产指标样本。')).toBeInTheDocument();
    expect(screen.getByText('暂无 promotion candidates。可以调整 user_id、limit 或 min_confidence 后刷新。')).toBeInTheDocument();
    expect(screen.getByText('暂无最近注入解释。后端未接入可查询 store 时会返回空结果。')).toBeInTheDocument();
  });

  it('normalizes null arrays returned by action endpoints', async () => {
    render(<MemoryGovernance />);

    await waitFor(() => {
      expect(mockClient.adminGetMemoryGovernance).toHaveBeenCalled();
    });

    fireEvent.click(screen.getByText('Dry-run'));
    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith('success', 'Dry-run 匹配 0 条');
    });
    expect(screen.getByText('没有命中待删除 memory。')).toBeInTheDocument();

    fireEvent.click(screen.getByText('导出'));
    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith('success', '已导出 0 条 memory');
    });

    fireEvent.click(screen.getByText('生成 vector-space plan'));
    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith('success', 'Vector-space dry-run 命中 0 条');
    });
  });
});
