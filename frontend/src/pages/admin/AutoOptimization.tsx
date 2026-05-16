import { useCallback, useEffect, useState } from 'react';
import { CheckCircle2, FileText, Filter, PlayCircle, RefreshCcw, RotateCcw, ShieldAlert, Sparkles, XCircle } from 'lucide-react';
import { useNodeClient } from '../../hooks/useNodeClient';
import { useToastStore } from '../../store/toast';
import type {
  ABReportResponse,
  BatchEvalRun,
  EvalDiff,
  EvalRun,
  EvalResult,
  OptimizationApprovalRecord,
  OptimizationReviewSuggestion,
  OptimizationSuggestionStatus,
  QualityCandidateRecord,
  RunnerInfo,
  RollbackAlert,
  RollbackRecord,
} from '../../types/api';

const card = 'rounded-2xl border border-[var(--border-color)] bg-[var(--bg-card)] p-4 shadow-sm';
const button = 'inline-flex items-center justify-center gap-2 px-3 py-2 rounded-[10px] border border-[var(--border-color)] text-sm text-[var(--text-primary)] hover:bg-[var(--bg-secondary)] disabled:opacity-50 disabled:cursor-not-allowed transition-colors duration-150';
const successButton = 'inline-flex items-center justify-center gap-2 px-3 py-2 rounded-[10px] border border-emerald-200 text-sm text-emerald-700 hover:bg-emerald-50 disabled:opacity-50 disabled:cursor-not-allowed transition-colors duration-150';
const dangerButton = 'inline-flex items-center justify-center gap-2 px-3 py-2 rounded-[10px] border border-red-200 text-sm text-red-700 hover:bg-red-50 disabled:opacity-50 disabled:cursor-not-allowed transition-colors duration-150';

const statuses: Array<{ value: OptimizationSuggestionStatus | ''; label: string }> = [
  { value: '', label: '全部状态' },
  { value: 'pending', label: '待审批' },
  { value: 'approved', label: '已批准' },
  { value: 'rejected', label: '已拒绝' },
  { value: 'expired', label: '已过期' },
];

export function AutoOptimization() {
  const client = useNodeClient();
  const addToast = useToastStore((s) => s.addToast);
  const [suggestions, setSuggestions] = useState<OptimizationReviewSuggestion[]>([]);
  const [candidates, setCandidates] = useState<QualityCandidateRecord[]>([]);
  const [batchEvals, setBatchEvals] = useState<BatchEvalRun[]>([]);
  const [status, setStatus] = useState<OptimizationSuggestionStatus | ''>('pending');
  const [selected, setSelected] = useState<OptimizationReviewSuggestion | null>(null);
  const [note, setNote] = useState('');
  const [loading, setLoading] = useState(true);
  const [rollbackStatus, setRollbackStatus] = useState<string | null>(null);
  const [evalDiff, setEvalDiff] = useState<EvalDiff | null>(null);
  const [abReport, setABReport] = useState<ABReportResponse | null>(null);
  const [approvals, setApprovals] = useState<OptimizationApprovalRecord[]>([]);
  const [rollbackAlerts, setRollbackAlerts] = useState<RollbackAlert[]>([]);
  const [rollbacks, setRollbacks] = useState<RollbackRecord[]>([]);

  const rememberCandidate = useCallback((candidate?: QualityCandidateRecord | null) => {
    if (!candidate) return;
    setCandidates((current) => {
      const existing = current.find((item) => item.id === candidate.id);
      if (!existing) return [candidate, ...current];
      return current.map((item) => item.id === candidate.id ? { ...item, ...candidate } : item);
    });
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [suggestionRes, candidateRes, batchEvalRes] = await Promise.all([
        client.adminListOptimizationSuggestions({ status, page: 1, size: 100 }),
        client.adminListQualityCandidates({ status: 'approved', page: 1, size: 30 }),
        client.adminListBatchEvals(),
      ]);
      const next = suggestionRes.suggestions ?? suggestionRes.items ?? [];
      setSuggestions(next);
      setCandidates(candidateRes.candidates ?? []);
      setBatchEvals(batchEvalRes.items ?? []);
      setSelected((current) => current ? next.find((item) => item.id === current.id) ?? null : next[0] ?? null);
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '加载优化建议失败');
    } finally {
      setLoading(false);
    }
  }, [client, status, addToast]);

  useEffect(() => { load(); }, [load]);

  const refreshOptimizationGuards = async (subjectId?: string) => {
    try {
      const [approvalRes, alertRes, rollbackRes] = await Promise.all([
        client.adminListOptimizationApprovals(subjectId ? { subjectId } : {}),
        client.adminListRollbackAlerts(),
        client.adminListRollbacks(),
      ]);
      setApprovals(approvalRes.items ?? []);
      setRollbackAlerts(alertRes.items ?? []);
      setRollbacks(rollbackRes.items ?? []);
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '加载审批/回滚记录失败');
    }
  };

  const computeLatestEvalDiff = async () => {
    const runsWithCases = batchEvals.filter((run) => (run.case_results?.length ?? 0) > 0);
    if (runsWithCases.length < 2) {
      addToast('warning', '至少需要两次带 case_results 的 batch eval run 才能计算 eval diff');
      return;
    }
    const [treatmentRun, baselineRun] = runsWithCases;
    try {
      const diff = await client.adminComputeEvalDiff({
        baseline: batchEvalRunToEvalRun(baselineRun),
        treatment: batchEvalRunToEvalRun(treatmentRun),
      });
      setEvalDiff(diff);
      setABReport(null);
      await refreshOptimizationGuards(diff.id);
      addToast('success', '已基于最近两次 batch eval 计算 eval diff');
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '计算 eval diff 失败');
    }
  };

  const generateFromEvalDiff = async () => {
    if (!evalDiff) return;
    try {
      await client.adminGenerateEvalDiffSuggestions(evalDiff);
      addToast('success', '已从 eval diff 生成优化建议');
      await load();
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '生成 eval diff 建议失败');
    }
  };

  const loadABReport = async () => {
    if (!evalDiff) return;
    try {
      const report = await client.adminGetABReport(evalDiff.id);
      setABReport(report);
      addToast('success', '已生成 AB report');
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '生成 AB report 失败');
    }
  };

  const createApproval = async () => {
    const subjectId = evalDiff?.id ?? selected?.id;
    if (!subjectId) return;
    const guard = evalDiff ? evidenceGuard(evalDiff.treatment_runner_info) : evidenceGuard(selected?.runner_info);
    if (evalDiff && !guard.canApprove) {
      addToast('warning', guard.message);
      return;
    }
    try {
      const approval = await client.adminCreateOptimizationApproval({
        subject_id: subjectId,
        subject_type: evalDiff ? 'eval_diff' : 'suggestion',
        action: 'approve',
        reviewer_role: 'lead',
        note: note || '前端最小入口记录',
      });
      rememberCandidate(approval.candidate);
      addToast('success', '已创建审批记录');
      await refreshOptimizationGuards(subjectId);
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '创建审批记录失败');
    }
  };

  const evaluateRollbackAlert = async () => {
    if (!evalDiff) return;
    try {
      const res = await client.adminEvaluateRollbackAlert({
        eval_diff: evalDiff,
        thresholds: {
          min_success_rate_delta: -0.05,
          max_latency_delta_ms: 500,
        },
      });
      addToast(res.triggered ? 'success' : 'info', res.triggered ? '已触发 rollback alert' : '未触发 rollback alert');
      await refreshOptimizationGuards(evalDiff.id);
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '评估 rollback alert 失败');
    }
  };

  const generateFromCandidate = async (candidate: QualityCandidateRecord) => {
    try {
      await client.adminGenerateOptimizationSuggestions(candidate.id);
      addToast('success', '已生成候选优化建议');
      await load();
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '生成优化建议失败');
    }
  };

  const review = async (action: 'approve' | 'reject') => {
    if (!selected) return;
    const guard = evidenceGuard(selected.runner_info);
    if (action === 'approve' && selected.source_eval_diff_id && !guard.canApprove) {
      addToast('warning', guard.message);
      return;
    }
    try {
      const updated = action === 'approve'
        ? await client.adminApproveOptimizationSuggestion(selected.id, note)
        : await client.adminRejectOptimizationSuggestion(selected.id, note);
      setSelected(updated);
      rememberCandidate(updated.candidate);
      addToast('success', action === 'approve' ? '已批准；不会自动写生产配置' : '已拒绝建议');
      setNote('');
      await load();
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '审批失败');
    }
  };

  const applySuggestion = async () => {
    if (!selected) return;
    const guard = evidenceGuard(selected.runner_info);
    if (selected.source_eval_diff_id && !guard.canApply) {
      addToast('warning', guard.message);
      return;
    }
    try {
      const updated = await client.adminApplyOptimizationSuggestion(selected.id);
      setSelected(updated);
      rememberCandidate(updated.candidate);
      addToast('success', '已应用建议并记录审计状态');
      await load();
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '应用建议失败');
      await load();
    }
  };

  const rollbackSuggestion = async () => {
    if (!selected) return;
    try {
      const rollout = await client.adminRollbackOptimizationSuggestion(selected.id);
      rememberCandidate(rollout.candidate);
      setRollbackStatus(`${rollout.status}${rollout.rolled_back_at ? ` · ${new Date(rollout.rolled_back_at).toLocaleString()}` : ''}`);
      addToast('success', '已回滚应用结果');
      await load();
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '回滚建议失败');
      await load();
    }
  };

  return (
    <div className="p-6 max-w-7xl mx-auto space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold text-[var(--text-primary)] font-display">自动优化闭环</h1>
          <p className="mt-1 text-sm text-[var(--text-secondary)]">
            从失败候选生成 prompt/tool/skill 建议，先审批再人工点击应用；每次应用都会记录 rollout，可一键回滚。
          </p>
        </div>
        <button onClick={load} className={button} disabled={loading}>
          <RefreshCcw size={14} />
          刷新
        </button>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-[340px_minmax(0,1fr)] gap-5">
        <section className={card}>
          <div className="flex items-center gap-2 mb-3">
            <Filter size={16} className="text-[var(--accent-600)]" />
            <h2 className="text-sm font-semibold text-[var(--text-primary)]">建议列表</h2>
          </div>
          <select
            value={status}
            onChange={(e) => setStatus(e.target.value as OptimizationSuggestionStatus | '')}
            className="w-full mb-3 px-3 py-2 rounded-lg border border-[var(--border-color)] bg-[var(--bg-primary)] text-sm text-[var(--text-primary)]"
          >
            {statuses.map((item) => <option key={item.value || 'all'} value={item.value}>{item.label}</option>)}
          </select>
          <div className="space-y-2 max-h-[620px] overflow-auto">
            {suggestions.length === 0 ? (
              <p className="py-8 text-center text-sm text-[var(--text-secondary)]">{loading ? '加载中...' : '暂无建议'}</p>
            ) : suggestions.map((item) => (
              <button
                key={item.id}
                onClick={() => {
                  setSelected(item);
                  setRollbackStatus(null);
                }}
                className={`w-full text-left p-3 rounded-lg border border-[var(--border-color)] hover:bg-[var(--bg-secondary)] ${selected?.id === item.id ? 'bg-[var(--bg-secondary)]' : ''}`}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="text-sm font-medium text-[var(--text-primary)] truncate">{item.title || item.id}</span>
                  <div className="flex items-center gap-1">
                    <StatusBadge status={item.status} />
                    <ApplyBadge status={item.apply_status || 'unapplied'} />
                  </div>
                </div>
                <p className="mt-1 text-xs text-[var(--text-secondary)] truncate">{item.target} · {item.kind}</p>
              </button>
            ))}
          </div>
        </section>

        <section className={card}>
          {selected ? (
            <div className="space-y-4">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                  <h2 className="text-base font-semibold text-[var(--text-primary)]">{selected.title}</h2>
                  <p className="text-xs text-[var(--text-secondary)] font-mono">{selected.id}</p>
                </div>
                <div className="flex items-center gap-2">
                  <StatusBadge status={selected.status} />
                  <ApplyBadge status={selected.apply_status || 'unapplied'} />
                </div>
              </div>
              <EvidenceGuardPanel runnerInfo={selected.runner_info} source={selected.source_eval_diff_id ? 'eval diff' : 'candidate'} />
              <p className="text-sm text-[var(--text-secondary)]">{selected.rationale}</p>
              {selected.apply_error ? (
                <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
                  应用状态：{selected.apply_status} · {selected.apply_error}
                </div>
              ) : selected.apply_status === 'applied' ? (
                <div className="rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm text-emerald-700">
                  已应用{selected.applied_at ? ` · ${new Date(selected.applied_at).toLocaleString()}` : ''}
                </div>
              ) : null}
              {rollbackStatus ? (
                <div className="rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 text-sm text-slate-700">
                  最近回滚：{rollbackStatus}
                </div>
              ) : null}
              <div className="grid grid-cols-1 xl:grid-cols-2 gap-3">
                <DiffBlock title="Current" value={selected.current_value || '(empty)'} />
                <DiffBlock title="Proposed" value={selected.proposed_value || '(empty)'} />
              </div>
              <textarea
                value={note}
                onChange={(e) => setNote(e.target.value)}
                rows={3}
                placeholder="审批备注，例如：通过离线 eval，手动合并到 prompt 后再观察"
                className="w-full px-3 py-2 rounded-lg border border-[var(--border-color)] bg-[var(--bg-primary)] text-sm text-[var(--text-primary)] placeholder:text-[var(--text-secondary)]"
              />
              <div className="flex flex-wrap gap-2">
                <button className={successButton} disabled={selected.status !== 'pending'} onClick={() => review('approve')}>
                  <CheckCircle2 size={14} />
                  记录批准
                </button>
                <button className={dangerButton} disabled={selected.status !== 'pending'} onClick={() => review('reject')}>
                  <XCircle size={14} />
                  拒绝
                </button>
                {selected.status === 'approved' && (selected.apply_status || 'unapplied') === 'unapplied' ? (
                  <button className={successButton} onClick={applySuggestion}>
                    <PlayCircle size={14} />
                    执行应用
                  </button>
                ) : null}
                {(selected.apply_status || 'unapplied') === 'applied' ? (
                  <button
                    className={button}
                    onClick={() => {
                      if (confirm('确认回滚该优化建议的已应用变更？')) void rollbackSuggestion();
                    }}
                  >
                    <RotateCcw size={14} />
                    回滚
                  </button>
                ) : null}
              </div>
            </div>
          ) : (
            <p className="py-16 text-center text-sm text-[var(--text-secondary)]">选择一条优化建议查看详情。</p>
          )}
        </section>
      </div>

      <section className={card}>
        <div className="flex items-center gap-2 mb-3">
          <Sparkles size={16} className="text-[var(--accent-600)]" />
          <h2 className="text-sm font-semibold text-[var(--text-primary)]">从已通过候选生成建议</h2>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3">
          {candidates.length === 0 ? (
            <p className="text-sm text-[var(--text-secondary)]">暂无 approved 候选。</p>
          ) : candidates.map((candidate) => (
            <div key={candidate.id} className="rounded-lg border border-[var(--border-color)] p-3">
              <p className="text-sm font-mono text-[var(--text-primary)] truncate">{candidate.id}</p>
              <p className="mt-1 text-xs text-[var(--text-secondary)] truncate">{candidate.failure_type} · {candidate.input}</p>
              <button className={`${button} mt-3 w-full`} onClick={() => generateFromCandidate(candidate)}>
                <Sparkles size={14} />
                生成建议
              </button>
            </div>
          ))}
        </div>
      </section>

      <section className={card}>
        <div className="flex flex-wrap items-center justify-between gap-3 mb-3">
          <div className="flex items-center gap-2">
            <FileText size={16} className="text-[var(--accent-600)]" />
            <h2 className="text-sm font-semibold text-[var(--text-primary)]">Eval Diff / AB / Rollback Guard</h2>
          </div>
          <div className="flex flex-wrap gap-2">
            <button className={button} onClick={computeLatestEvalDiff}>
              <RefreshCcw size={14} />
              计算最新 diff
            </button>
            <button className={button} disabled={!evalDiff} onClick={generateFromEvalDiff}>
              <Sparkles size={14} />
              diff 生成建议
            </button>
            <button className={button} disabled={!evalDiff} onClick={loadABReport}>
              <FileText size={14} />
              AB report
            </button>
            <button className={button} disabled={!evalDiff && !selected} onClick={createApproval}>
              <CheckCircle2 size={14} />
              记录审批
            </button>
            <button className={button} disabled={!evalDiff} onClick={evaluateRollbackAlert}>
              <ShieldAlert size={14} />
              评估 rollback alert
            </button>
          </div>
        </div>
        <div className="grid grid-cols-1 xl:grid-cols-3 gap-3">
          <div className="rounded-lg border border-[var(--border-color)] p-3">
            <p className="text-xs text-[var(--text-secondary)]">当前 Eval Diff</p>
            {evalDiff ? (
              <div className="mt-2 space-y-1 text-sm text-[var(--text-primary)]">
                <p className="font-mono text-xs truncate">{evalDiff.id}</p>
                <p>success Δ {formatPercent(evalDiff.success_rate_delta)} · latency Δ {evalDiff.average_latency_delta_ms.toFixed(0)}ms</p>
                <p className="text-xs text-[var(--text-secondary)]">case diffs: {evalDiff.case_diffs.length}</p>
                <RunnerEvidenceInline label="baseline" runnerInfo={evalDiff.baseline_runner_info} />
                <RunnerEvidenceInline label="treatment" runnerInfo={evalDiff.treatment_runner_info} />
              </div>
            ) : (
              <p className="mt-2 text-sm text-[var(--text-secondary)]">基于最近两次带 case_results 的 batch eval 计算后，可生成建议、AB report 和 rollback alert。</p>
            )}
          </div>
          <div className="rounded-lg border border-[var(--border-color)] p-3">
            <p className="text-xs text-[var(--text-secondary)]">Approvals / Rollbacks</p>
            <p className="mt-2 text-sm text-[var(--text-primary)]">approvals: {approvals.length} · rollbacks: {rollbacks.length}</p>
            <div className="mt-2 max-h-28 overflow-auto space-y-1">
              {approvals.slice(0, 4).map((item) => (
                <p key={item.id} className="text-xs text-[var(--text-secondary)] truncate">{item.action} · {item.subject_type} · {item.subject_id}</p>
              ))}
            </div>
          </div>
          <div className="rounded-lg border border-[var(--border-color)] p-3">
            <p className="text-xs text-[var(--text-secondary)]">Rollback Alerts</p>
            <p className="mt-2 text-sm text-[var(--text-primary)]">open alerts: {rollbackAlerts.filter((item) => item.status === 'open').length}</p>
            <div className="mt-2 max-h-28 overflow-auto space-y-1">
              {rollbackAlerts.slice(0, 4).map((item) => (
                <p key={item.id} className="text-xs text-[var(--text-secondary)] truncate">{item.status} · {item.reasons.join(', ')}</p>
              ))}
            </div>
          </div>
        </div>
        {abReport ? (
          <pre className="mt-3 max-h-56 overflow-auto rounded-lg border border-[var(--border-color)] bg-[var(--bg-secondary)] p-3 text-xs text-[var(--text-primary)] whitespace-pre-wrap">
            {abReport.markdown}
          </pre>
        ) : null}
      </section>
    </div>
  );
}

function StatusBadge({ status }: { status: OptimizationSuggestionStatus }) {
  const cls = status === 'approved'
    ? 'bg-emerald-100 text-emerald-700'
    : status === 'rejected'
      ? 'bg-red-100 text-red-700'
      : status === 'expired'
        ? 'bg-zinc-100 text-zinc-700'
        : 'bg-amber-100 text-amber-700';
  return <span className={`px-2 py-0.5 rounded-full text-[11px] font-medium ${cls}`}>{status}</span>;
}

function ApplyBadge({ status }: { status: OptimizationReviewSuggestion['apply_status'] }) {
  const cls = status === 'applied'
    ? 'bg-emerald-100 text-emerald-700'
    : status === 'apply_error'
      ? 'bg-red-100 text-red-700'
      : status === 'not_applicable'
        ? 'bg-zinc-100 text-zinc-700'
        : 'bg-slate-100 text-slate-600';
  return <span className={`px-2 py-0.5 rounded-full text-[11px] font-medium ${cls}`}>{status}</span>;
}

function EvidenceGuardPanel({ runnerInfo, source }: { runnerInfo?: RunnerInfo; source: string }) {
  const guard = evidenceGuard(runnerInfo);
  const isEvalDiff = source === 'eval diff';
  const cls = guard.level === 'static_schema' || guard.level === 'unknown'
    ? 'border-red-200 bg-red-50 text-red-700'
    : guard.canApply
      ? 'border-emerald-200 bg-emerald-50 text-emerald-700'
      : 'border-amber-200 bg-amber-50 text-amber-700';
  return (
    <div className={`rounded-lg border px-3 py-2 text-sm ${cls}`}>
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xs uppercase tracking-wide">证据级别</span>
        <span className="font-mono text-xs">{guard.level}</span>
        <span className="font-mono text-xs">{runnerLabel(runnerInfo)}</span>
      </div>
      <p className="mt-1 text-xs">{isEvalDiff ? guard.message : '候选生成建议未绑定 eval diff runner；需人工审批，不能据此判断可上线。'}</p>
    </div>
  );
}

function RunnerEvidenceInline({ label, runnerInfo }: { label: string; runnerInfo?: RunnerInfo }) {
  const guard = evidenceGuard(runnerInfo);
  return (
    <p className="text-xs text-[var(--text-secondary)]">
      {label} runner <span className="font-mono text-[var(--text-primary)]">{runnerLabel(runnerInfo)}</span>
      {' '}· evidence <span className={guard.canApprove ? 'font-mono text-emerald-700' : 'font-mono text-red-700'}>{guard.level}</span>
    </p>
  );
}

function DiffBlock({ title, value }: { title: string; value: string }) {
  return (
    <div>
      <p className="mb-1 text-xs text-[var(--text-secondary)]">{title}</p>
      <pre className="min-h-[220px] max-h-[360px] overflow-auto rounded-lg border border-[var(--border-color)] bg-[var(--bg-secondary)] p-3 text-xs text-[var(--text-primary)] whitespace-pre-wrap">{value}</pre>
    </div>
  );
}

function formatPercent(value: number) {
  return `${(value * 100).toFixed(1)}%`;
}

function batchEvalRunToEvalRun(run: BatchEvalRun): EvalRun {
  return {
    id: run.id,
    runner_info: run.runner_info,
    created_at: run.created_at,
    results: (run.case_results ?? []).map((result): EvalResult => ({
      case_id: result.case_id,
      passed: result.passed,
      cost_usd: 0,
      latency_ms: 0,
      failure_type: result.passed ? 'none' : 'runtime',
      reason: result.reason,
    })),
  };
}

function evidenceGuard(runnerInfo?: RunnerInfo) {
  const level = normalizeEvidenceLevel(runnerInfo?.evidence_level);
  const canApprove = level === 'real_runner' || level === 'production_shadow' || level === 'human_verified';
  const canApply = canApprove;
  if (level === 'static_schema') {
    return {
      level,
      canApprove,
      canApply,
      message: 'static_schema 仅代表静态结构检查，不能批准优化，也不能作为上线或执行应用依据。',
    };
  }
  if (level === 'unknown') {
    return {
      level,
      canApprove,
      canApply,
      message: '未返回 runner evidence，不能批准优化，也不能作为执行应用依据。',
    };
  }
  if (canApprove) {
    return {
      level,
      canApprove,
      canApply,
      message: '具备真实执行证据；仍需审批记录和回滚观察，不代表自动可上线。',
    };
  }
  return {
    level,
    canApprove,
    canApply,
    message: `${level} 是有限证据，需补充 real_runner 或 production_shadow 后再批准或执行应用。`,
  };
}

function normalizeEvidenceLevel(level?: string) {
  const trimmed = level?.trim();
  return trimmed || 'unknown';
}

function runnerLabel(runnerInfo?: RunnerInfo) {
  if (!runnerInfo?.name) return 'runner:-';
  return `${runnerInfo.name}${runnerInfo.version ? `@${runnerInfo.version}` : ''}`;
}
