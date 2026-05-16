import { useCallback, useEffect, useMemo, useState } from 'react';
import { Activity, AlertTriangle, Brain, CheckCircle2, Download, Filter, Hash, RefreshCcw, ShieldAlert, ShieldX, Trash2, Upload } from 'lucide-react';
import { useNodeClient } from '../../hooks/useNodeClient';
import { useToastStore } from '../../store/toast';
import type { EmbeddingBacklogStats, MemoryExportDocument, MemoryGovernanceStats, MemoryInjectionExplainItem, MemoryInjectionExplainResponse, MemoryProductionMetricAlert, MemoryProductionMetrics, MemoryProductionMetricsSeriesPoint, MemoryProductionMetricsSnapshot, MemoryPromotionApplyResponse, MemoryPromotionCandidate, MemoryPromotionCandidatesResponse, MemoryPruneResponse, OptimizationApprovalRecord, VectorSpaceMigrationPlan, VectorSpaceMigrationResponse, VectorSpaceMigrationUpdate } from '../../types/api';

const card = 'rounded-2xl border border-[var(--border-color)] bg-[var(--bg-card)] p-4 shadow-sm';
const button = 'inline-flex items-center justify-center gap-2 px-3 py-2 rounded-[10px] border border-[var(--border-color)] text-sm text-[var(--text-primary)] hover:bg-[var(--bg-secondary)] disabled:opacity-50 disabled:cursor-not-allowed transition-colors duration-150';
const dangerButton = 'inline-flex items-center justify-center gap-2 px-3 py-2 rounded-[10px] border border-red-200 text-sm text-red-700 hover:bg-red-50 disabled:opacity-50 disabled:cursor-not-allowed transition-colors duration-150';

export function MemoryGovernance() {
  const client = useNodeClient();
  const addToast = useToastStore((s) => s.addToast);
  const [stats, setStats] = useState<MemoryGovernanceStats | null>(null);
  const [backlogStats, setBacklogStats] = useState<EmbeddingBacklogStats | null>(null);
  const [injectionExplain, setInjectionExplain] = useState<MemoryInjectionExplainResponse | null>(null);
  const [productionMetrics, setProductionMetrics] = useState<MemoryProductionMetrics | null>(null);
  const [promotionCandidates, setPromotionCandidates] = useState<MemoryPromotionCandidatesResponse | null>(null);
  const [promotionApprovals, setPromotionApprovals] = useState<Record<string, OptimizationApprovalRecord[]>>({});
  const [lastPromotionApply, setLastPromotionApply] = useState<MemoryPromotionApplyResponse | null>(null);
  const [lastPlan, setLastPlan] = useState<MemoryPruneResponse | null>(null);
  const [exportJSON, setExportJSON] = useState('');
  const [importJSON, setImportJSON] = useState('');
  const [vectorPlan, setVectorPlan] = useState<VectorSpaceMigrationResponse | null>(null);
  const [targetSpace, setTargetSpace] = useState('memory:default');
  const [filterUserId, setFilterUserId] = useState('');
  const [filterTarget, setFilterTarget] = useState('');
  const [filterTargetScope, setFilterTargetScope] = useState('');
  const [filterKind, setFilterKind] = useState('');
  const [promotionLimit, setPromotionLimit] = useState(20);
  const [promotionMinConfidence, setPromotionMinConfidence] = useState('');
  const [applyingSubjectID, setApplyingSubjectID] = useState<string | null>(null);
  const [approvingSubjectID, setApprovingSubjectID] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const minConfidence = stats?.policy?.min_confidence ?? stats?.min_confidence;
  const maxMemories = stats?.policy?.max_memories ?? stats?.max_memories;
  const promotionConfidence = parseOptionalNumber(promotionMinConfidence) ?? minConfidence;
  const activeFilter = useMemo(() => ({
    userId: filterUserId || undefined,
    target: filterTarget || undefined,
    target_scope: filterTargetScope || undefined,
    kind: filterKind || undefined,
  }), [filterKind, filterTarget, filterTargetScope, filterUserId]);
  const normalizedBacklogStats = useMemo(() => normalizeEmbeddingBacklogStats(backlogStats), [backlogStats]);
  const normalizedInjectionExplain = useMemo(() => normalizeMemoryInjectionExplain(injectionExplain), [injectionExplain]);
  const normalizedProductionMetrics = useMemo(() => normalizeMemoryProductionMetrics(productionMetrics), [productionMetrics]);
  const normalizedPromotionCandidates = useMemo(() => normalizeMemoryPromotionCandidates(promotionCandidates), [promotionCandidates]);
  const normalizedLastPlan = useMemo(() => normalizeMemoryPruneResponse(lastPlan), [lastPlan]);
  const normalizedVectorPlan = useMemo(() => normalizeVectorSpaceMigrationResponse(vectorPlan), [vectorPlan]);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [governance, backlog, explain, metrics, promotions] = await Promise.all([
        client.adminGetMemoryGovernance(5000, activeFilter),
        client.adminGetEmbeddingBacklogStats(),
        client.adminGetMemoryInjectionExplain({ limit: 10 }),
        client.adminGetMemoryProductionMetrics({ windowMinutes: 1440, bucketMinutes: 60 }).catch(() => null),
        client.adminListMemoryPromotionCandidates({
          ...activeFilter,
          limit: promotionLimit,
          minConfidence: promotionConfidence,
        }).catch(() => null),
      ]);
      setStats(governance);
      const nextPromotions = normalizeMemoryPromotionCandidates(promotions);
      setBacklogStats(normalizeEmbeddingBacklogStats(backlog));
      setInjectionExplain(normalizeMemoryInjectionExplain(explain));
      setProductionMetrics(normalizeMemoryProductionMetrics(metrics));
      setPromotionCandidates(nextPromotions);
      if (nextPromotions.items.length) {
        const approvalPairs = await Promise.all(nextPromotions.items.map(async (item) => {
          try {
            const res = await client.adminListOptimizationApprovals({ subjectId: item.subject_id });
            return [item.subject_id, res.items ?? []] as const;
          } catch {
            return [item.subject_id, []] as const;
          }
        }));
        setPromotionApprovals(Object.fromEntries(approvalPairs));
      } else {
        setPromotionApprovals({});
      }
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '加载 Memory 治理失败');
    } finally {
      setLoading(false);
    }
  }, [client, addToast, activeFilter, promotionConfidence, promotionLimit]);

  useEffect(() => { load(); }, [load]);

  const prune = async (dryRun: boolean) => {
    try {
      const res = await client.adminPruneMemoryGovernance({
        dryRun,
        minConfidence,
        maxMemories: maxMemories && maxMemories > 0 ? maxMemories : undefined,
        limit: 5000,
        ...activeFilter,
      });
      const normalized = normalizeMemoryPruneResponse(res);
      setLastPlan(normalized);
      addToast('success', dryRun ? `Dry-run 匹配 ${normalized?.delete_ids.length ?? 0} 条` : `已删除 ${normalized?.deleted ?? normalized?.delete_ids.length ?? 0} 条`);
      await load();
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '执行治理剪枝失败');
    }
  };

  const exportMemory = async () => {
    try {
      const doc = normalizeMemoryExportDocument(await client.adminExportMemory({ limit: 5000, ...activeFilter }));
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
      const res = await client.adminImportMemory({
        user_id: filterUserId || undefined,
        target: filterTarget || undefined,
        target_scope: filterTargetScope || undefined,
        kind: filterKind || undefined,
        reset_ids: true,
        document,
      });
      addToast('success', `已导入 ${res.imported} 条 memory`);
      await load();
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '导入 memory 失败');
    }
  };

  const planVectorSpace = async () => {
    try {
      const plan = normalizeVectorSpaceMigrationResponse(await client.adminPlanVectorSpaceMigration({
        target_space: targetSpace || 'memory:default',
        batch_size: 25,
        dry_run: true,
        limit: 5000,
      }));
      setVectorPlan(plan);
      addToast('success', `Vector-space dry-run 命中 ${plan?.updated ?? 0} 条`);
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '生成 vector-space plan 失败');
    }
  };

  const applyPromotion = async (subjectID: string) => {
    const approval = latestApprovedPromotion(promotionApprovals[subjectID]);
    if (!approval) {
      addToast('error', '请先记录 lead/admin 审批，再应用 promotion');
      return;
    }
    setApplyingSubjectID(subjectID);
    try {
      const res = await client.adminApplyMemoryPromotion({
        subject_id: subjectID,
        user_id: filterUserId || undefined,
        target: filterTarget || undefined,
        target_scope: filterTargetScope || undefined,
        memory_kind: filterKind || undefined,
        limit: promotionLimit,
        min_confidence: promotionConfidence,
        approval_id: approval.id,
      });
      setLastPromotionApply(res);
      addToast('success', `已应用 promotion：memory #${res.memory_id}`);
      await load();
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '应用 promotion 失败');
    } finally {
      setApplyingSubjectID(null);
    }
  };

  const approvePromotion = async (subjectID: string) => {
    setApprovingSubjectID(subjectID);
    try {
      const approval = await client.adminCreateOptimizationApproval({
        subject_id: subjectID,
        subject_type: 'memory_promotion',
        action: 'approve',
        reviewer_role: 'lead',
        note: 'Memory promotion approved from Admin Governance',
      });
      setPromotionApprovals((current) => ({
        ...current,
        [subjectID]: [...(current[subjectID] ?? []), approval],
      }));
      addToast('success', '已记录 promotion 审批');
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '记录 promotion 审批失败');
    } finally {
      setApprovingSubjectID(null);
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

      <section className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-sm font-semibold text-[var(--text-primary)]">生产监控</h2>
            <p className="mt-1 text-sm text-[var(--text-secondary)]">
              数据源：{normalizedProductionMetrics.source} · 窗口：{normalizedProductionMetrics.window_minutes} 分钟
            </p>
          </div>
          <Activity size={18} className="text-[var(--accent-600)]" />
        </div>
        {normalizedProductionMetrics.alerts.length ? (
          <div className="space-y-2">
            {normalizedProductionMetrics.alerts.map((alert) => (
              <div key={alert.code} className="flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900">
                <AlertTriangle size={15} className="mt-0.5 shrink-0" />
                <span>{alert.message} · {formatNumber(alert.value)}</span>
              </div>
            ))}
          </div>
        ) : null}
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-3">
          <Metric title="Dropped" value={normalizedProductionMetrics.snapshot.embedding_dropped_total} icon={<AlertTriangle size={17} />} />
          <Metric title="Fallback" value={normalizedProductionMetrics.snapshot.hybrid_search_fallback_total} icon={<Activity size={17} />} />
          <Metric title="Vector Mismatch" value={normalizedProductionMetrics.snapshot.vector_space_mismatch_total} icon={<ShieldAlert size={17} />} />
          <Metric title="Backlog Depth" value={normalizedProductionMetrics.snapshot.backlog_depth_total || normalizedBacklogStats.total} icon={<Activity size={17} />} />
          <Metric title="Embed p95 Sec" value={normalizedProductionMetrics.snapshot.embedding_latency_p95_seconds} icon={<Activity size={17} />} />
        </div>
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-3">
          <Breakdown title="drop reasons" items={normalizedProductionMetrics.snapshot.drop_reasons} />
          <Breakdown title="fallback reasons" items={normalizedProductionMetrics.snapshot.fallback_reasons} />
          <Breakdown title="mismatch operations" items={normalizedProductionMetrics.snapshot.mismatch_operations} />
        </div>
        <div className="overflow-auto rounded-lg border border-[var(--border-color)]">
          <table className="min-w-full text-sm">
            <thead className="bg-[var(--bg-secondary)] text-xs text-[var(--text-secondary)]">
              <tr>
                <th className="px-3 py-2 text-left font-medium">bucket</th>
                <th className="px-3 py-2 text-right font-medium">dropped</th>
                <th className="px-3 py-2 text-right font-medium">fallback</th>
                <th className="px-3 py-2 text-right font-medium">mismatch</th>
                <th className="px-3 py-2 text-right font-medium">latency avg</th>
                <th className="px-3 py-2 text-right font-medium">backlog</th>
              </tr>
            </thead>
            <tbody>
              {normalizedProductionMetrics.series.slice(-8).map((point) => (
                <tr key={point.since} className="border-t border-[var(--border-color)]">
                  <td className="px-3 py-2 text-[var(--text-secondary)]">{formatHour(point.since)}</td>
                  <td className="px-3 py-2 text-right text-[var(--text-primary)]">{formatNumber(point.embedding_dropped_total)}</td>
                  <td className="px-3 py-2 text-right text-[var(--text-primary)]">{formatNumber(point.hybrid_search_fallback_total)}</td>
                  <td className="px-3 py-2 text-right text-[var(--text-primary)]">{formatNumber(point.vector_space_mismatch_total)}</td>
                  <td className="px-3 py-2 text-right text-[var(--text-primary)]">{formatSeconds(point.embedding_latency_avg_seconds)}</td>
                  <td className="px-3 py-2 text-right text-[var(--text-primary)]">{formatNumber(point.backlog_depth_total)}</td>
                </tr>
              ))}
              {!normalizedProductionMetrics.series.length ? (
                <tr>
                  <td colSpan={6} className="px-3 py-4 text-center text-[var(--text-secondary)]">暂无生产指标样本。</td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
      </section>

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
            <p className="mt-1 text-2xl font-semibold text-[var(--text-primary)]">{normalizedBacklogStats.total}</p>
            <div className="mt-3 flex flex-wrap gap-2">
              {Object.entries(normalizedBacklogStats.by_state).map(([key, value]) => (
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
            {normalizedVectorPlan ? (
              <div className="rounded-lg border border-[var(--border-color)] p-3">
                <p className="text-sm text-[var(--text-primary)]">
                  dry_run={String(normalizedVectorPlan.plan.dry_run)} · scanned={normalizedVectorPlan.plan.scanned} · updates={normalizedVectorPlan.updated}
                  {normalizedVectorPlan.plan.resume_token ? ` · resume=${normalizedVectorPlan.plan.resume_token}` : ''}
                </p>
                <div className="mt-2 max-h-28 overflow-auto space-y-1">
                  {normalizedVectorPlan.plan.updates.slice(0, 8).map((update) => (
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
        <div className="flex flex-wrap items-center justify-between gap-3 mb-3">
          <div>
            <h2 className="text-sm font-semibold text-[var(--text-primary)]">Promotion 候选</h2>
            <p className="mt-1 text-sm text-[var(--text-secondary)]">
              查看可提升为 procedural memory 的候选项；应用前必须先存在 memory_promotion 审批记录。
            </p>
          </div>
          <button className={button} onClick={() => void load()} disabled={loading}>
            <RefreshCcw size={14} />
            刷新候选
          </button>
        </div>
        <div className="mb-3 grid grid-cols-1 sm:grid-cols-3 gap-3">
          <FilterField label="user_id" value={filterUserId} onChange={setFilterUserId} placeholder="可选，跟随上方筛选" icon={<Filter size={14} />} />
          <NumberField label="limit" value={promotionLimit} onChange={setPromotionLimit} min={1} max={200} />
          <label className="block">
            <span className="mb-1 flex items-center gap-2 text-xs text-[var(--text-secondary)]"><ShieldAlert size={14} />min_confidence</span>
            <input
              value={promotionMinConfidence}
              onChange={(e) => setPromotionMinConfidence(e.target.value)}
              placeholder={minConfidence == null ? '后端默认' : formatPolicyValue(minConfidence)}
              className="w-full rounded-lg border border-[var(--border-color)] bg-[var(--bg-primary)] px-3 py-2 text-sm text-[var(--text-primary)]"
            />
          </label>
        </div>
        {lastPromotionApply ? (
          <div className="mb-3 flex items-start gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm text-emerald-900">
            <CheckCircle2 size={15} className="mt-0.5 shrink-0" />
            <span>
              最近应用：{lastPromotionApply.subject_id} → memory #{lastPromotionApply.memory_id}
              {lastPromotionApply.approval_id ? ` · approval ${lastPromotionApply.approval_id}` : ''}
            </span>
          </div>
        ) : null}
        <div className="space-y-3">
          {normalizedPromotionCandidates.items.length ? (
            normalizedPromotionCandidates.items.map((item) => {
              const approval = latestApprovedPromotion(promotionApprovals[item.subject_id]);
              return (
                <div key={`${item.subject_id}-${item.created_at}`} className="rounded-lg border border-[var(--border-color)] bg-[var(--bg-primary)] p-3">
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="min-w-0">
                      <p className="font-mono text-sm text-[var(--text-primary)] truncate">{item.subject_id}</p>
                      <p className="mt-1 text-xs text-[var(--text-secondary)]">
                        {item.source_kind || 'unknown'} · 置信度 {formatNumber(item.confidence)} · {formatDateTime(item.created_at)}
                      </p>
                      <p className="mt-1 text-xs text-[var(--text-secondary)]">
                        {approval ? `已审批：${approval.id}` : '未审批'}
                      </p>
                    </div>
                    <div className="flex flex-wrap gap-2">
                      <button
                        className={button}
                        onClick={() => void approvePromotion(item.subject_id)}
                        disabled={approvingSubjectID === item.subject_id || Boolean(approval)}
                      >
                        <ShieldAlert size={14} />
                        {approval ? '已审批' : approvingSubjectID === item.subject_id ? '审批中' : '记录审批'}
                      </button>
                      <button
                        className={button}
                        onClick={() => void applyPromotion(item.subject_id)}
                        disabled={applyingSubjectID === item.subject_id || !approval}
                      >
                        <CheckCircle2 size={14} />
                        {applyingSubjectID === item.subject_id ? '应用中' : '应用'}
                      </button>
                    </div>
                  </div>
                  <p className="mt-3 whitespace-pre-wrap text-sm text-[var(--text-primary)]">{item.proposed_procedural_memory?.content ?? 'n/a'}</p>
                  <p className="mt-2 text-sm text-[var(--text-secondary)]">{item.rationale || '暂无 rationale'}</p>
                  <p className="mt-2 text-xs text-[var(--text-secondary)]">source_memory_ids: {formatIdList(item.source_memory_ids)}</p>
                </div>
              );
            })
          ) : (
            <p className="rounded-lg border border-dashed border-[var(--border-color)] px-3 py-4 text-sm text-[var(--text-secondary)]">
              暂无 promotion candidates。可以调整 user_id、limit 或 min_confidence 后刷新。
            </p>
          )}
        </div>
      </section>

      <section className={card}>
        <div className="flex flex-wrap items-center justify-between gap-3 mb-3">
          <div>
            <h2 className="text-sm font-semibold text-[var(--text-primary)]">最近注入解释</h2>
            <p className="mt-1 text-sm text-[var(--text-secondary)]">
              展示最近的 memory 注入摘要和跳过计数，不展开完整 memory 内容。
            </p>
          </div>
          <button className={button} onClick={() => void load()}>
            <RefreshCcw size={14} />
            刷新解释
          </button>
        </div>
        <div className="grid grid-cols-1 lg:grid-cols-[280px_minmax(0,1fr)] gap-3">
          <div className="rounded-lg border border-[var(--border-color)] bg-[var(--bg-secondary)] p-3 space-y-2">
            <FilterField label="user_id" value={filterUserId} onChange={setFilterUserId} placeholder="u1" icon={<Filter size={14} />} />
            <FilterField label="target" value={filterTarget} onChange={setFilterTarget} placeholder="profile" icon={<Hash size={14} />} />
            <FilterField label="target_scope" value={filterTargetScope} onChange={setFilterTargetScope} placeholder="personal" icon={<ShieldX size={14} />} />
            <FilterField label="kind" value={filterKind} onChange={setFilterKind} placeholder="user" icon={<Brain size={14} />} />
            <div className="pt-1">
              <button className={button} onClick={() => void load()} disabled={loading}>
                <RefreshCcw size={14} />
                应用筛选
              </button>
            </div>
          </div>
          <div className="space-y-3">
            {normalizedInjectionExplain.items.length ? (
              normalizedInjectionExplain.items.map((item, idx) => (
                <div key={`${item.timestamp ?? 'item'}-${idx}`} className="rounded-lg border border-[var(--border-color)] bg-[var(--bg-primary)] p-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <p className="text-sm text-[var(--text-primary)]">
                      {item.route || 'unknown route'}
                      {item.session_id_hash ? ` · ${item.session_id_hash}` : ''}
                    </p>
                    <p className="text-xs text-[var(--text-secondary)]">{item.timestamp ? new Date(item.timestamp).toLocaleString() : 'unknown time'}</p>
                  </div>
                  <div className="mt-2 flex flex-wrap gap-2">
                    <SummaryPill label="selected" value={item.memory_ids.length} />
                    <SummaryPill label="skipped" value={skipTotal(item.skipped_memory_ids, item.skip_counts)} />
                    <SummaryPill label="tokens" value={item.estimated_tokens ?? 0} />
                    <SummaryPill label="injected" value={item.memory_injected ? 1 : 0} />
                  </div>
                  <div className="mt-2 grid grid-cols-1 sm:grid-cols-2 gap-2 text-xs text-[var(--text-secondary)]">
                    <p>memory_ids: {formatIdList(item.memory_ids)}</p>
                    <p>skipped_memory_ids: {formatIdList(item.skipped_memory_ids)}</p>
                    <p>prompt_versions: {item.prompt_versions.join(', ') || 'n/a'}</p>
                    <p>contamination_check: {item.contamination_check || 'n/a'}</p>
                    <p>memory_domain: {item.memory_domain_id || 'generic'}</p>
                    <p>memory_source: {[item.memory_source_kind, item.memory_source_name].filter(Boolean).join('/') || 'n/a'}</p>
                    <p>memory_owner: {[item.memory_owner_scope, item.memory_owner_id].filter(Boolean).join(':') || 'n/a'}</p>
                  </div>
                  <div className="mt-2 flex flex-wrap gap-2">
                    {Object.entries(item.skip_counts).map(([key, value]) => (
                      <span key={key} className="px-2 py-1 rounded-full bg-[var(--bg-secondary)] text-xs text-[var(--text-secondary)]">
                        {key}: {value}
                      </span>
                    ))}
                  </div>
                </div>
              ))
            ) : (
              <p className="rounded-lg border border-dashed border-[var(--border-color)] px-3 py-4 text-sm text-[var(--text-secondary)]">
                暂无最近注入解释。后端未接入可查询 store 时会返回空结果。
              </p>
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

      {normalizedLastPlan && (
        <section className={card}>
          <h2 className="text-sm font-semibold text-[var(--text-primary)] mb-3">最近一次计划</h2>
          <p className="text-sm text-[var(--text-secondary)] mb-3">
            dry_run={String(normalizedLastPlan.dry_run)} · matched={normalizedLastPlan.matched ?? normalizedLastPlan.delete_ids.length} · deleted={normalizedLastPlan.deleted ?? 0}
          </p>
          <div className="max-h-72 overflow-auto rounded-lg border border-[var(--border-color)]">
            {normalizedLastPlan.delete_ids.length === 0 ? (
              <p className="p-4 text-sm text-[var(--text-secondary)]">没有命中待删除 memory。</p>
            ) : normalizedLastPlan.delete_ids.map((id) => (
              <div key={id} className="flex items-center justify-between gap-3 px-3 py-2 border-b border-[var(--border-color)] last:border-b-0">
                <span className="font-mono text-sm text-[var(--text-primary)]">#{id}</span>
                <span className="text-xs text-[var(--text-secondary)]">{normalizedLastPlan.reasons[String(id)] || normalizedLastPlan.reasons[id] || 'unknown'}</span>
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

function FilterField({
  label,
  value,
  onChange,
  placeholder,
  icon,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder: string;
  icon: React.ReactNode;
}) {
  return (
    <label className="block">
      <span className="mb-1 flex items-center gap-2 text-xs text-[var(--text-secondary)]">{icon}{label}</span>
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="w-full rounded-lg border border-[var(--border-color)] bg-[var(--bg-primary)] px-3 py-2 text-sm text-[var(--text-primary)]"
      />
    </label>
  );
}

function NumberField({
  label,
  value,
  onChange,
  min,
  max,
}: {
  label: string;
  value: number;
  onChange: (value: number) => void;
  min: number;
  max: number;
}) {
  return (
    <label className="block">
      <span className="mb-1 flex items-center gap-2 text-xs text-[var(--text-secondary)]"><Hash size={14} />{label}</span>
      <input
        type="number"
        min={min}
        max={max}
        value={value}
        onChange={(e) => onChange(clampNumber(Number(e.target.value), min, max))}
        className="w-full rounded-lg border border-[var(--border-color)] bg-[var(--bg-primary)] px-3 py-2 text-sm text-[var(--text-primary)]"
      />
    </label>
  );
}

function SummaryPill({ label, value }: { label: string; value: number }) {
  return (
    <span className="rounded-full border border-[var(--border-color)] px-2 py-1 text-xs text-[var(--text-secondary)]">
      {label}: {value}
    </span>
  );
}

type NormalizedMemoryInjectionExplainItem = Omit<MemoryInjectionExplainItem, 'memory_injected' | 'memory_ids' | 'prompt_versions' | 'skip_counts' | 'skipped_memory_ids'> & {
  memory_injected: boolean;
  memory_ids: number[];
  prompt_versions: string[];
  skip_counts: Record<string, number>;
  skipped_memory_ids: number[];
};

type NormalizedMemoryInjectionExplainResponse = Omit<MemoryInjectionExplainResponse, 'items'> & {
  items: NormalizedMemoryInjectionExplainItem[];
};

type NormalizedEmbeddingBacklogStats = Omit<EmbeddingBacklogStats, 'by_state'> & {
  by_state: Record<string, number>;
};

type NormalizedMemoryProductionMetricsSnapshot = Omit<MemoryProductionMetricsSnapshot, 'backlog_depth_by_status' | 'drop_reasons' | 'fallback_reasons' | 'mismatch_operations'> & {
  backlog_depth_by_status: Record<string, number>;
  drop_reasons: Record<string, number>;
  fallback_reasons: Record<string, number>;
  mismatch_operations: Record<string, number>;
};

type NormalizedMemoryProductionMetrics = {
  source: string;
  since: string;
  until: string;
  window_minutes: number;
  snapshot: NormalizedMemoryProductionMetricsSnapshot;
  series: MemoryProductionMetricsSeriesPoint[];
  alerts: MemoryProductionMetricAlert[];
};

type NormalizedMemoryPromotionCandidate = Omit<MemoryPromotionCandidate, 'proposed_procedural_memory' | 'rationale' | 'source_kind' | 'source_memory_ids'> & {
  proposed_procedural_memory?: MemoryPromotionCandidate['proposed_procedural_memory'];
  rationale: string;
  source_kind: string;
  source_memory_ids: number[];
};

type NormalizedMemoryPromotionCandidatesResponse = Omit<MemoryPromotionCandidatesResponse, 'items'> & {
  items: NormalizedMemoryPromotionCandidate[];
};

type NormalizedMemoryPruneResponse = Omit<MemoryPruneResponse, 'delete_ids' | 'reasons'> & {
  delete_ids: number[];
  reasons: Record<string, string>;
};

type NormalizedVectorSpaceMigrationPlan = Omit<VectorSpaceMigrationPlan, 'updates'> & {
  updates: VectorSpaceMigrationUpdate[];
};

type NormalizedVectorSpaceMigrationResponse = Omit<VectorSpaceMigrationResponse, 'plan'> & {
  plan: NormalizedVectorSpaceMigrationPlan;
};

function normalizeEmbeddingBacklogStats(stats: EmbeddingBacklogStats | null): NormalizedEmbeddingBacklogStats {
  return {
    total: stats?.total ?? 0,
    by_state: { ...(stats?.by_state ?? {}) },
  };
}

function normalizeMemoryInjectionExplain(response: MemoryInjectionExplainResponse | null): NormalizedMemoryInjectionExplainResponse {
  const items = (response?.items ?? []).map((item) => ({
    ...item,
    memory_injected: item.memory_injected ?? false,
    memory_ids: [...(item.memory_ids ?? [])],
    prompt_versions: [...(item.prompt_versions ?? [])],
    skip_counts: { ...(item.skip_counts ?? {}) },
    skipped_memory_ids: [...(item.skipped_memory_ids ?? [])],
  }));
  return {
    items,
    total: response?.total ?? items.length,
    limit: response?.limit ?? items.length,
    source: response?.source,
  };
}

function normalizeMemoryProductionMetrics(metrics: MemoryProductionMetrics | null): NormalizedMemoryProductionMetrics {
  const now = new Date().toISOString();
  const snapshot = metrics?.snapshot;
  return {
    source: metrics?.source || '未连接',
    since: metrics?.since || '',
    until: metrics?.until || now,
    window_minutes: metrics?.window_minutes ?? 1440,
    snapshot: {
      embedding_dropped_total: snapshot?.embedding_dropped_total ?? 0,
      hybrid_search_fallback_total: snapshot?.hybrid_search_fallback_total ?? 0,
      vector_space_mismatch_total: snapshot?.vector_space_mismatch_total ?? 0,
      embedding_latency_count: snapshot?.embedding_latency_count ?? 0,
      embedding_latency_avg_seconds: snapshot?.embedding_latency_avg_seconds ?? 0,
      embedding_latency_p95_seconds: snapshot?.embedding_latency_p95_seconds ?? 0,
      backlog_depth_total: snapshot?.backlog_depth_total ?? 0,
      backlog_depth_by_status: { ...(snapshot?.backlog_depth_by_status ?? {}) },
      drop_reasons: { ...(snapshot?.drop_reasons ?? {}) },
      fallback_reasons: { ...(snapshot?.fallback_reasons ?? {}) },
      mismatch_operations: { ...(snapshot?.mismatch_operations ?? {}) },
    },
    series: [...(metrics?.series ?? [])],
    alerts: [...(metrics?.alerts ?? [])],
  };
}

function normalizeMemoryPromotionCandidates(response: MemoryPromotionCandidatesResponse | null): NormalizedMemoryPromotionCandidatesResponse {
  const items = (response?.items ?? []).map((item) => ({
    ...item,
    rationale: item.rationale ?? '',
    source_kind: item.source_kind ?? '',
    source_memory_ids: [...(item.source_memory_ids ?? [])],
  }));
  return {
    items,
    total: response?.total ?? items.length,
    limit: response?.limit ?? items.length,
  };
}

function normalizeMemoryPruneResponse(response: MemoryPruneResponse | null): NormalizedMemoryPruneResponse | null {
  if (!response) return null;
  return {
    ...response,
    delete_ids: [...(response.delete_ids ?? [])],
    reasons: { ...(response.reasons ?? {}) },
  };
}

function normalizeMemoryExportDocument(document: MemoryExportDocument): Omit<MemoryExportDocument, 'memories' | 'version'> & { memories: NonNullable<MemoryExportDocument['memories']>; version: number } {
  return {
    ...document,
    version: document.version ?? 1,
    memories: [...(document.memories ?? [])],
  };
}

function normalizeVectorSpaceMigrationResponse(response: VectorSpaceMigrationResponse | null): NormalizedVectorSpaceMigrationResponse | null {
  if (!response) return null;
  return {
    ...response,
    plan: {
      dry_run: response.plan?.dry_run ?? false,
      scanned: response.plan?.scanned ?? 0,
      resume_token: response.plan?.resume_token,
      next_offset: response.plan?.next_offset,
      updates: [...(response.plan?.updates ?? [])],
    },
  };
}

function skipTotal(ids?: number[], counts?: Record<string, number>) {
  if (counts?.total != null) return counts.total;
  return (ids?.length ?? 0) + Object.entries(counts ?? {}).reduce((sum, [key, value]) => (
    key === 'total' ? sum : sum + value
  ), 0);
}

function latestApprovedPromotion(approvals?: OptimizationApprovalRecord[]) {
  const approved = (approvals ?? []).filter((approval) => (
    approval.subject_type === 'memory_promotion' && approval.action === 'approve'
  ));
  return approved.at(-1);
}

function formatIdList(ids?: number[]) {
  if (!ids || ids.length === 0) return 'n/a';
  return ids.slice(0, 6).map((id) => `#${id}`).join(', ');
}

function Metric({ title, value, icon }: { title: string; value: number; icon: React.ReactNode }) {
  return (
    <div className={card}>
      <div className="flex items-center justify-between">
        <p className="text-xs text-[var(--text-secondary)]">{title}</p>
        <span className="text-[var(--accent-600)]">{icon}</span>
      </div>
      <p className="mt-2 text-2xl font-semibold text-[var(--text-primary)]">{formatNumber(value)}</p>
    </div>
  );
}

function Breakdown({ title, items }: { title: string; items?: Record<string, number> }) {
  const entries = Object.entries(items ?? {}).filter(([, value]) => value > 0);
  return (
    <div className="rounded-lg border border-[var(--border-color)] bg-[var(--bg-secondary)] p-3">
      <p className="text-xs text-[var(--text-secondary)]">{title}</p>
      <div className="mt-2 flex flex-wrap gap-2">
        {entries.length ? entries.map(([key, value]) => (
          <span key={key} className="rounded-full bg-[var(--bg-primary)] px-2 py-1 text-xs text-[var(--text-secondary)]">
            {key}: {formatNumber(value)}
          </span>
        )) : (
          <span className="text-xs text-[var(--text-secondary)]">no signal</span>
        )}
      </div>
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

function formatNumber(value: number) {
  if (!Number.isFinite(value)) return '0';
  if (Math.abs(value) >= 100) return Math.round(value).toLocaleString();
  if (Number.isInteger(value)) return value.toLocaleString();
  return value.toFixed(3).replace(/0+$/, '').replace(/\.$/, '');
}

function formatSeconds(value: number) {
  if (!Number.isFinite(value) || value <= 0) return '0s';
  return `${formatNumber(value)}s`;
}

function formatDateTime(value: string) {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value || 'unknown time';
  return parsed.toLocaleString();
}

function formatHour(value: string) {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

function parseOptionalNumber(value: string) {
  if (!value.trim()) return undefined;
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : undefined;
}

function clampNumber(value: number, min: number, max: number) {
  if (!Number.isFinite(value)) return min;
  return Math.min(max, Math.max(min, value));
}
