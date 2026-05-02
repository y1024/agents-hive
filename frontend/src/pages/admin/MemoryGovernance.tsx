import { useCallback, useEffect, useState } from 'react';
import { Brain, Download, RefreshCcw, ShieldAlert, Trash2, Upload } from 'lucide-react';
import { useNodeClient } from '../../hooks/useNodeClient';
import { useToastStore } from '../../store/toast';
import type { EmbeddingBacklogStats, MemoryGovernanceStats, MemoryPruneResponse, VectorSpaceMigrationResponse } from '../../types/api';

const card = 'rounded-2xl border border-[var(--border-color)] bg-[var(--bg-card)] p-4 shadow-sm';
const button = 'inline-flex items-center justify-center gap-2 px-3 py-2 rounded-[10px] border border-[var(--border-color)] text-sm text-[var(--text-primary)] hover:bg-[var(--bg-secondary)] disabled:opacity-50 disabled:cursor-not-allowed transition-colors duration-150';
const dangerButton = 'inline-flex items-center justify-center gap-2 px-3 py-2 rounded-[10px] border border-red-200 text-sm text-red-700 hover:bg-red-50 disabled:opacity-50 disabled:cursor-not-allowed transition-colors duration-150';

export function MemoryGovernance() {
  const client = useNodeClient();
  const addToast = useToastStore((s) => s.addToast);
  const [stats, setStats] = useState<MemoryGovernanceStats | null>(null);
  const [backlogStats, setBacklogStats] = useState<EmbeddingBacklogStats | null>(null);
  const [lastPlan, setLastPlan] = useState<MemoryPruneResponse | null>(null);
  const [exportJSON, setExportJSON] = useState('');
  const [importJSON, setImportJSON] = useState('');
  const [vectorPlan, setVectorPlan] = useState<VectorSpaceMigrationResponse | null>(null);
  const [targetSpace, setTargetSpace] = useState('memory:default');
  const [loading, setLoading] = useState(true);
  const minConfidence = stats?.policy?.min_confidence ?? stats?.min_confidence;
  const maxMemories = stats?.policy?.max_memories ?? stats?.max_memories;

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [governance, backlog] = await Promise.all([
        client.adminGetMemoryGovernance(5000),
        client.adminGetEmbeddingBacklogStats(),
      ]);
      setStats(governance);
      setBacklogStats(backlog);
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '加载 Memory 治理失败');
    } finally {
      setLoading(false);
    }
  }, [client, addToast]);

  useEffect(() => { load(); }, [load]);

  const prune = async (dryRun: boolean) => {
    try {
      const res = await client.adminPruneMemoryGovernance({
        dryRun,
        minConfidence,
        maxMemories: maxMemories && maxMemories > 0 ? maxMemories : undefined,
        limit: 5000,
      });
      setLastPlan(res);
      addToast('success', dryRun ? `Dry-run 匹配 ${res.delete_ids.length} 条` : `已删除 ${res.deleted ?? res.delete_ids.length} 条`);
      await load();
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '执行治理剪枝失败');
    }
  };

  const exportMemory = async () => {
    try {
      const doc = await client.adminExportMemory({ limit: 5000 });
      const text = JSON.stringify(doc, null, 2);
      setExportJSON(text);
      setImportJSON((current) => current || text);
      addToast('success', `已导出 ${doc.memories.length} 条 memory`);
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '导出 memory 失败');
    }
  };

  const importMemory = async () => {
    try {
      const document = JSON.parse(importJSON);
      const res = await client.adminImportMemory({ reset_ids: true, document });
      addToast('success', `已导入 ${res.imported} 条 memory`);
      await load();
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '导入 memory 失败');
    }
  };

  const planVectorSpace = async () => {
    try {
      const plan = await client.adminPlanVectorSpaceMigration({
        target_space: targetSpace || 'memory:default',
        batch_size: 25,
        dry_run: true,
        limit: 5000,
      });
      setVectorPlan(plan);
      addToast('success', `Vector-space dry-run 命中 ${plan.updated} 条`);
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '生成 vector-space plan 失败');
    }
  };

  return (
    <div className="p-6 max-w-6xl mx-auto space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold text-[var(--text-primary)] font-display">Memory 生产治理</h1>
          <p className="mt-1 text-sm text-[var(--text-secondary)]">
            统计缺失治理元数据、过期、低置信和跨用户风险；删除动作默认 dry-run，执行删除需要显式确认。
          </p>
        </div>
        <button onClick={load} className={button} disabled={loading}>
          <RefreshCcw size={14} />
          刷新
        </button>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-3">
        <Metric title="Total" value={stats?.total ?? 0} icon={<Brain size={17} />} />
        <Metric title="Missing Governance" value={stats?.missing_governance ?? 0} icon={<ShieldAlert size={17} />} />
        <Metric title="Expired" value={stats?.expired ?? 0} icon={<ShieldAlert size={17} />} />
        <Metric title="Low Confidence" value={stats?.low_confidence ?? 0} icon={<ShieldAlert size={17} />} />
        <Metric title="Cross-user Risk" value={stats?.cross_user_risk ?? 0} icon={<ShieldAlert size={17} />} />
      </div>

      <section className={card}>
        <div className="flex flex-wrap items-center justify-between gap-3 mb-3">
          <div>
            <h2 className="text-sm font-semibold text-[var(--text-primary)]">Embedding Backlog / Vector Space</h2>
            <p className="mt-1 text-sm text-[var(--text-secondary)]">
              查看 embedding backlog 状态，并对 memory vector-space 迁移生成 dry-run 计划。
            </p>
          </div>
          <button className={button} onClick={planVectorSpace}>
            <RefreshCcw size={14} />
            生成 vector-space plan
          </button>
        </div>
        <div className="grid grid-cols-1 lg:grid-cols-[260px_minmax(0,1fr)] gap-3">
          <div className="rounded-lg border border-[var(--border-color)] bg-[var(--bg-secondary)] p-3">
            <p className="text-xs text-[var(--text-secondary)]">backlog total</p>
            <p className="mt-1 text-2xl font-semibold text-[var(--text-primary)]">{backlogStats?.total ?? 0}</p>
            <div className="mt-3 flex flex-wrap gap-2">
              {Object.entries(backlogStats?.by_state ?? {}).map(([key, value]) => (
                <span key={key} className="px-2 py-1 rounded-full bg-[var(--bg-primary)] text-xs text-[var(--text-secondary)]">{key}: {value}</span>
              ))}
            </div>
          </div>
          <div className="space-y-3">
            <div className="flex flex-wrap gap-2">
              <input
                value={targetSpace}
                onChange={(e) => setTargetSpace(e.target.value)}
                className="min-w-[220px] flex-1 px-3 py-2 rounded-lg border border-[var(--border-color)] bg-[var(--bg-primary)] text-sm text-[var(--text-primary)]"
                placeholder="memory:default"
              />
            </div>
            {vectorPlan ? (
              <div className="rounded-lg border border-[var(--border-color)] p-3">
                <p className="text-sm text-[var(--text-primary)]">
                  dry_run={String(vectorPlan.plan.dry_run)} · scanned={vectorPlan.plan.scanned} · updates={vectorPlan.updated}
                  {vectorPlan.plan.resume_token ? ` · resume=${vectorPlan.plan.resume_token}` : ''}
                </p>
                <div className="mt-2 max-h-28 overflow-auto space-y-1">
                  {vectorPlan.plan.updates.slice(0, 8).map((update) => (
                    <p key={update.memory_id} className="text-xs text-[var(--text-secondary)] truncate">memory #{update.memory_id} · {update.record.content}</p>
                  ))}
                </div>
              </div>
            ) : (
              <p className="text-sm text-[var(--text-secondary)]">尚未生成 vector-space 迁移计划。</p>
            )}
          </div>
        </div>
      </section>

      <section className={card}>
        <h2 className="text-sm font-semibold text-[var(--text-primary)] mb-2">剪枝计划</h2>
        <p className="text-sm text-[var(--text-secondary)] mb-4">
          策略：删除过期 memory；删除置信度低于当前策略阈值的 memory；如果策略返回 max_memories，则按该容量上限治理。
        </p>
        <div className="mb-4 grid grid-cols-1 sm:grid-cols-2 gap-3">
          <PolicyField label="min_confidence" value={minConfidence == null ? '后端默认/持久化策略' : formatPolicyValue(minConfidence)} />
          <PolicyField label="max_memories" value={maxMemories == null ? '后端默认/持久化策略' : maxMemories > 0 ? String(maxMemories) : '未设置'} />
        </div>
        <div className="flex flex-wrap gap-2">
          <button className={button} onClick={() => prune(true)}>
            <RefreshCcw size={14} />
            Dry-run
          </button>
          <button
            className={dangerButton}
            onClick={() => {
              if (confirm('确认删除 dry-run 命中的 memory？该操作不可逆。')) void prune(false);
            }}
          >
            <Trash2 size={14} />
            执行删除
          </button>
        </div>
      </section>

      {lastPlan && (
        <section className={card}>
          <h2 className="text-sm font-semibold text-[var(--text-primary)] mb-3">最近一次计划</h2>
          <p className="text-sm text-[var(--text-secondary)] mb-3">
            dry_run={String(lastPlan.dry_run)} · matched={lastPlan.matched ?? lastPlan.delete_ids.length} · deleted={lastPlan.deleted ?? 0}
          </p>
          <div className="max-h-72 overflow-auto rounded-lg border border-[var(--border-color)]">
            {lastPlan.delete_ids.length === 0 ? (
              <p className="p-4 text-sm text-[var(--text-secondary)]">没有命中待删除 memory。</p>
            ) : lastPlan.delete_ids.map((id) => (
              <div key={id} className="flex items-center justify-between gap-3 px-3 py-2 border-b border-[var(--border-color)] last:border-b-0">
                <span className="font-mono text-sm text-[var(--text-primary)]">#{id}</span>
                <span className="text-xs text-[var(--text-secondary)]">{lastPlan.reasons?.[String(id)] || lastPlan.reasons?.[id] || 'unknown'}</span>
              </div>
            ))}
          </div>
        </section>
      )}

      <section className={card}>
        <div className="flex flex-wrap items-center justify-between gap-3 mb-3">
          <div>
            <h2 className="text-sm font-semibold text-[var(--text-primary)]">Memory Export / Import</h2>
            <p className="mt-1 text-sm text-[var(--text-secondary)]">导出 JSON 可直接作为导入文档；导入默认 reset_ids，避免覆盖原 ID。</p>
          </div>
          <div className="flex flex-wrap gap-2">
            <button className={button} onClick={exportMemory}>
              <Download size={14} />
              导出
            </button>
            <button className={button} onClick={importMemory} disabled={!importJSON.trim()}>
              <Upload size={14} />
              导入
            </button>
          </div>
        </div>
        <div className="grid grid-cols-1 xl:grid-cols-2 gap-3">
          <div>
            <p className="mb-1 text-xs text-[var(--text-secondary)]">最近导出</p>
            <textarea
              value={exportJSON}
              readOnly
              rows={12}
              className="w-full rounded-lg border border-[var(--border-color)] bg-[var(--bg-secondary)] p-3 font-mono text-xs text-[var(--text-primary)]"
              placeholder="点击导出后显示 JSON"
            />
          </div>
          <div>
            <p className="mb-1 text-xs text-[var(--text-secondary)]">导入文档</p>
            <textarea
              value={importJSON}
              onChange={(e) => setImportJSON(e.target.value)}
              rows={12}
              className="w-full rounded-lg border border-[var(--border-color)] bg-[var(--bg-primary)] p-3 font-mono text-xs text-[var(--text-primary)]"
              placeholder='{"version":1,"memories":[]}'
            />
          </div>
        </div>
      </section>
    </div>
  );
}

function Metric({ title, value, icon }: { title: string; value: number; icon: React.ReactNode }) {
  return (
    <div className={card}>
      <div className="flex items-center justify-between">
        <p className="text-xs text-[var(--text-secondary)]">{title}</p>
        <span className="text-[var(--accent-600)]">{icon}</span>
      </div>
      <p className="mt-2 text-2xl font-semibold text-[var(--text-primary)]">{value}</p>
    </div>
  );
}

function PolicyField({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-[var(--border-color)] bg-[var(--bg-secondary)] px-3 py-2">
      <p className="text-xs text-[var(--text-secondary)]">{label}</p>
      <p className="mt-1 font-mono text-sm text-[var(--text-primary)]">{value}</p>
    </div>
  );
}

function formatPolicyValue(value: number) {
  return Number.isInteger(value) ? String(value) : value.toFixed(2);
}
