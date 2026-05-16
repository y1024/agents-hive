import { useCallback, useEffect, useMemo, useState } from 'react';
import { BarChart3, FileText, GitBranch, Layers3, Play, RefreshCcw, RotateCcw, XCircle } from 'lucide-react';
import { useNodeClient } from '../../../hooks/useNodeClient';
import { useToastStore } from '../../../store/toast';
import type {
  BatchEvalRun,
  DomainRegressionStatus,
  GateMetrics,
  GroupingRule,
  GroupingRulePreview,
  QualityCandidateStatus,
  QualityDashboardSnapshot,
  QualityEvaluationVerdict,
  QualityReport,
  QualityWorkbenchCluster,
  QualityWorkbenchFilter,
  ReplayFanoutPlan,
  ReplayJob,
  RunnerInfo,
  ShadowEvalMetrics,
  ShadowEvalResult,
  VersionDiff,
} from '../../../types/api';

const card = 'rounded-2xl border border-[var(--border-color)] bg-[var(--bg-card)] p-4 shadow-sm';
const heroCard = 'rounded-2xl border border-[var(--accent-border)] bg-[var(--accent-subtle)] p-5';
const button = 'inline-flex items-center justify-center gap-2 px-3 py-2 rounded-[10px] border border-[var(--border-color)] text-sm text-[var(--text-primary)] hover:bg-[var(--bg-secondary)] disabled:opacity-50 disabled:cursor-not-allowed transition-colors duration-150';
const primaryButton = 'inline-flex items-center justify-center gap-2 px-3 py-2 rounded-[10px] bg-[var(--accent-600)] text-white text-sm hover:bg-[var(--accent-700)] disabled:opacity-50 disabled:cursor-not-allowed transition-colors duration-150';
const tabBase = 'inline-flex items-center gap-2 px-3 py-1.5 rounded-full text-sm transition-colors duration-150';
const tabActive = 'bg-[var(--accent-600)] text-white';
const tabIdle = 'text-[var(--text-secondary)] hover:bg-[var(--bg-secondary)]';

type WorkbenchTab = 'replay' | 'eval' | 'report' | 'distribution';
type WorkbenchFilterDraft = {
  status: QualityCandidateStatus | '';
  route: string;
  domain_id: string;
  source_kind: string;
  source_name: string;
  failure_type: string;
};

const emptyFilterDraft: WorkbenchFilterDraft = {
  status: '',
  route: '',
  domain_id: '',
  source_kind: '',
  source_name: '',
  failure_type: '',
};

export function QualityWorkbench() {
  const client = useNodeClient();
  const addToast = useToastStore((s) => s.addToast);
  const [clusters, setClusters] = useState<QualityWorkbenchCluster[]>([]);
  const [replays, setReplays] = useState<ReplayJob[]>([]);
  const [evals, setEvals] = useState<BatchEvalRun[]>([]);
  const [reports, setReports] = useState<QualityReport[]>([]);
  const [groupingRules, setGroupingRules] = useState<GroupingRule[]>([]);
  const [snapshot, setSnapshot] = useState<QualityDashboardSnapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [selectedCluster, setSelectedCluster] = useState<QualityWorkbenchCluster | null>(null);
  const [runningReplayId, setRunningReplayId] = useState<string | null>(null);
  const [groupingPreview, setGroupingPreview] = useState<GroupingRulePreview | null>(null);
  const [fanoutPlan, setFanoutPlan] = useState<ReplayFanoutPlan | null>(null);
  const [versionDiff, setVersionDiff] = useState<VersionDiff | null>(null);
  const [casesDir, setCasesDir] = useState('');
  const [activeTab, setActiveTab] = useState<WorkbenchTab>('replay');
  const [filterDraft, setFilterDraft] = useState<WorkbenchFilterDraft>(emptyFilterDraft);
  const [appliedFilter, setAppliedFilter] = useState<QualityWorkbenchFilter>({});

  const cleanDraftFilter = useMemo<QualityWorkbenchFilter>(() => {
    const next: QualityWorkbenchFilter = {};
    if (filterDraft.status) next.status = filterDraft.status;
    if (filterDraft.route.trim()) next.route = filterDraft.route.trim();
    if (filterDraft.domain_id.trim()) next.domain_id = filterDraft.domain_id.trim();
    if (filterDraft.source_kind.trim()) next.source_kind = filterDraft.source_kind.trim();
    if (filterDraft.source_name.trim()) next.source_name = filterDraft.source_name.trim();
    if (filterDraft.failure_type.trim()) next.failure_type = filterDraft.failure_type.trim();
    return next;
  }, [filterDraft]);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [clusterRes, replayRes, evalRes, reportRes, snap, rulesRes] = await Promise.all([
        client.adminListQualityWorkbenchClusters({ ...appliedFilter, page: 1, size: 100 }),
        client.adminListReplayJobs({ ...appliedFilter, page: 1, size: 50 }),
        client.adminListBatchEvals({ ...appliedFilter, page: 1, size: 50 }),
        client.adminListQualityReports(),
        client.adminGetQualityDashboardSnapshot(appliedFilter),
        client.adminListGroupingRules(),
      ]);
      const nextClusters = clusterRes.clusters ?? clusterRes.items ?? [];
      setClusters(nextClusters);
      setReplays(replayRes.items ?? []);
      setEvals(evalRes.items ?? []);
      setReports(reportRes.items ?? []);
      setGroupingRules(rulesRes.items ?? []);
      setSnapshot(snap);
      setSelectedCluster((current) => current ? nextClusters.find((c) => c.id === current.id) ?? null : nextClusters[0] ?? null);
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '加载质量工作台失败');
    } finally {
      setLoading(false);
    }
  }, [client, addToast, appliedFilter]);

  useEffect(() => { load(); }, [load]);

  const openCandidateCount = useMemo(() => {
    const counts = snapshot?.candidate_status_counts ?? {};
    return (counts.new ?? 0) + (counts.reviewing ?? 0);
  }, [snapshot]);

  const applyFilters = () => setAppliedFilter(cleanDraftFilter);
  const resetFilters = () => {
    setFilterDraft(emptyFilterDraft);
    setAppliedFilter({});
  };

  const createReplay = async (cluster: QualityWorkbenchCluster | null) => {
    if (!cluster) return;
    try {
      await client.adminCreateReplayJobs({
        kind: 'cluster',
        target_ids: [cluster.id],
        max_attempt: 1,
        ...(cluster.domain_id ? { domain_id: cluster.domain_id } : {}),
        ...(cluster.source_kind ? { source_kind: cluster.source_kind } : {}),
        ...(cluster.source_name ? { source_name: cluster.source_name } : {}),
      });
      addToast('success', '已加入 replay queue');
      await load();
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '创建 replay 失败');
    }
  };

  const previewGroupingRules = async () => {
    try {
      const preview = await client.adminPreviewGroupingRules();
      setGroupingPreview(preview);
      addToast('success', `Grouping preview: ${preview.clusters.length} clusters`);
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '预览 grouping rules 失败');
    }
  };

  const saveToolGroupingRule = async () => {
    const rule: GroupingRule = {
      id: 'tool_split',
      name: 'Tool Split',
      priority: 10,
      enabled: true,
      match: { failure_type: 'tool' },
      key_fields: ['failure_type', 'tool'],
      digest_normalize: ['path', 'num', 'uuid'],
      notes: '按实际工具拆分 tool failure 聚类',
    };
    try {
      await client.adminUpsertGroupingRule(rule.id, rule);
      addToast('success', '已保存 grouping rule');
      await load();
      const preview = await client.adminPreviewGroupingRules();
      setGroupingPreview(preview);
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '保存 grouping rule 失败');
    }
  };

  const deleteToolGroupingRule = async () => {
    try {
      await client.adminDeleteGroupingRule('tool_split');
      addToast('success', '已删除 grouping rule');
      await load();
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '删除 grouping rule 失败');
    }
  };

  const planReplayFanout = async () => {
    const targetIds = selectedCluster?.candidate_ids?.length ? selectedCluster.candidate_ids : clusters.map((cluster) => cluster.id);
    if (targetIds.length === 0) {
      addToast('warning', '没有可 fanout 的 cluster/candidate');
      return;
    }
    try {
      const plan = await client.adminPlanReplayFanout({ target_ids: targetIds, limit: 5 });
      setFanoutPlan(plan);
      addToast('success', `Replay fanout selected ${plan.selected_ids.length}/${plan.total}`);
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '生成 replay fanout 失败');
    }
  };

  const compareVersionDiff = async () => {
    const runsWithCases = evals.filter((run) => (run.case_results?.length ?? 0) > 0);
    if (runsWithCases.length < 2) {
      addToast('warning', '至少需要两次带 case_results 的 batch eval run 才能比较版本差异');
      return;
    }
    const [treatment, baseline] = runsWithCases;
    try {
      const diff = await client.adminCompareVersionMatrix({
        baseline_run_id: baseline.id,
        treatment_run_id: treatment.id,
        baseline: baseline.case_results ?? [],
        treatment: treatment.case_results ?? [],
      });
      setVersionDiff(diff);
      addToast('success', `Version diff: ${diff.regressed_case_ids.length} regressions`);
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '生成 version diff 失败');
    }
  };

  const runBatchEval = async () => {
    try {
      const trimmedCasesDir = casesDir.trim();
      await client.adminCreateBatchEval({ mode: 'manual', ...appliedFilter, ...(trimmedCasesDir ? { cases_dir: trimmedCasesDir } : {}) });
      addToast('success', '已创建批量评测 run');
      await load();
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '创建批量评测失败');
    }
  };

  const runReplay = async (job: ReplayJob) => {
    setRunningReplayId(job.id);
    try {
      const updated = await client.adminRunReplayJob(job.id);
      setReplays((current) => current.map((item) => item.id === updated.id ? updated : item));
      addToast(updated.status === 'succeeded' ? 'success' : 'error', updated.error || `Replay ${updated.status}`);
      await load();
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '运行 replay 失败');
      await load();
    } finally {
      setRunningReplayId(null);
    }
  };

  const generateReport = async () => {
    const now = new Date();
    const day = now.getDay() || 7;
    now.setDate(now.getDate() - day + 1);
    const weekStart = now.toISOString().slice(0, 10);
    try {
      await client.adminGenerateQualityReport(weekStart);
      addToast('success', '已生成周报');
      await load();
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '生成周报失败');
    }
  };

  return (
    <div className="p-6 max-w-7xl mx-auto space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold text-[var(--text-primary)] font-display">质量工作台</h1>
          <p className="mt-1 text-sm text-[var(--text-secondary)]">
            把失败候选聚类、回放、批量评测和周报放在同一个控制面；所有变更仍需人工确认。
          </p>
        </div>
        <button onClick={load} className={button} disabled={loading}>
          <RefreshCcw size={14} />
          刷新
        </button>
      </div>

      <HeroBand
        openClusters={snapshot?.open_clusters ?? clusters.filter((c) => c.open_count > 0).length}
        openCandidates={openCandidateCount}
        loading={loading}
      />

      <section className={card}>
        <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
          <h2 className="text-sm font-semibold text-[var(--text-primary)]">归因筛选</h2>
          <div className="flex flex-wrap gap-2">
            <button onClick={applyFilters} className={primaryButton} disabled={loading}>
              <RefreshCcw size={14} />
              应用
            </button>
            <button onClick={resetFilters} className={button} disabled={loading}>
              <XCircle size={14} />
              清空
            </button>
          </div>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-3 xl:grid-cols-6 gap-3">
          <FilterSelect
            label="Status"
            value={filterDraft.status}
            onChange={(value) => setFilterDraft((current) => ({ ...current, status: value as QualityCandidateStatus | '' }))}
            options={[
              ['', '全部'],
              ['new', 'new'],
              ['reviewing', 'reviewing'],
              ['approved', 'approved'],
              ['rejected', 'rejected'],
              ['promoted', 'promoted'],
              ['promoted_verified', 'promoted_verified'],
              ['promoted_regressed', 'promoted_regressed'],
            ]}
          />
          <FilterInput label="Route" value={filterDraft.route} onChange={(route) => setFilterDraft((current) => ({ ...current, route }))} placeholder="web" />
          <FilterInput label="Domain" value={filterDraft.domain_id} onChange={(domain_id) => setFilterDraft((current) => ({ ...current, domain_id }))} placeholder="generic" />
          <FilterInput label="Source kind" value={filterDraft.source_kind} onChange={(source_kind) => setFilterDraft((current) => ({ ...current, source_kind }))} placeholder="master" />
          <FilterInput label="Source name" value={filterDraft.source_name} onChange={(source_name) => setFilterDraft((current) => ({ ...current, source_name }))} placeholder="react" />
          <FilterSelect
            label="Failure"
            value={filterDraft.failure_type}
            onChange={(failure_type) => setFilterDraft((current) => ({ ...current, failure_type }))}
            options={[
              ['', '全部'],
              ['tool', 'tool'],
              ['permission', 'permission'],
              ['context', 'context'],
              ['prompt', 'prompt'],
              ['skill', 'skill'],
              ['model', 'model'],
              ['runtime', 'runtime'],
              ['user_input', 'user_input'],
            ]}
          />
        </div>
      </section>

      <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
        <Metric title="待处理 Candidates" value={openCandidateCount} icon={<BarChart3 size={17} />} accent />
        <Metric title="活跃 Clusters" value={snapshot?.open_clusters ?? clusters.filter((c) => c.open_count > 0).length} icon={<GitBranch size={17} />} />
        <Metric title="Replay Jobs" value={replays.length} icon={<Play size={17} />} />
        <Metric title="Batch Evals" value={evals.length} icon={<RotateCcw size={17} />} />
      </div>

      <div className="grid grid-cols-1 xl:grid-cols-[minmax(0,1.25fr)_minmax(360px,0.75fr)] gap-5">
        <section className={card}>
          <div className="flex items-center justify-between mb-3">
            <h2 className="text-sm font-semibold text-[var(--text-primary)]">失败聚类</h2>
            <button className={primaryButton} onClick={() => createReplay(selectedCluster)} disabled={!selectedCluster}>
              <Play size={14} />
              Replay selected
            </button>
          </div>
          <div className="overflow-hidden rounded-lg border border-[var(--border-color)]">
            <table className="w-full text-sm">
              <thead className="bg-[var(--bg-secondary)] text-[var(--text-secondary)]">
                <tr>
                  <th className="px-3 py-2 text-left font-medium">Cluster</th>
                  <th className="px-3 py-2 text-left font-medium">Failure</th>
                  <th className="px-3 py-2 text-left font-medium">Domain</th>
                  <th className="px-3 py-2 text-left font-medium">Source</th>
                  <th className="px-3 py-2 text-left font-medium">Tool/Skill</th>
                  <th className="px-3 py-2 text-left font-medium">Open</th>
                  <th className="px-3 py-2 text-left font-medium">Size</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[var(--border-color)]">
                {clusters.length === 0 ? (
                  <tr>
                    <td colSpan={7} className="px-3 py-10 text-center">
                      {loading ? (
                        <p className="text-sm text-[var(--text-secondary)]">加载中...</p>
                      ) : (
                        <div className="space-y-2">
                          <p className="text-sm font-medium text-[var(--text-primary)]">本周暂无失败聚类</p>
                          <p className="text-xs text-[var(--text-secondary)]">运行时若产生失败 candidate,会按 grouping rules 自动聚成 cluster 显示在此处。</p>
                          <button onClick={previewGroupingRules} className={`${button} mt-2`}>
                            <GitBranch size={14} />
                            查看 grouping rules
                          </button>
                        </div>
                      )}
                    </td>
                  </tr>
                ) : clusters.map((cluster) => (
                  <tr
                    key={cluster.id}
                    onClick={() => setSelectedCluster(cluster)}
                    className={`cursor-pointer hover:bg-[var(--bg-secondary)] ${selectedCluster?.id === cluster.id ? 'bg-[var(--bg-secondary)]' : ''}`}
                  >
                    <td className="px-3 py-2 font-mono text-xs text-[var(--text-primary)]">{cluster.id}</td>
                    <td className="px-3 py-2 text-[var(--text-primary)]">{cluster.failure_type || '-'}</td>
                    <td className="px-3 py-2 text-[var(--text-secondary)]">{cluster.domain_id || '-'}</td>
                    <td className="px-3 py-2 text-[var(--text-secondary)]">{[cluster.source_kind, cluster.source_name].filter(Boolean).join('/') || '-'}</td>
                    <td className="px-3 py-2 text-[var(--text-secondary)]">{cluster.tool || cluster.skill || '-'}</td>
                    <td className="px-3 py-2 text-[var(--text-primary)]">{cluster.open_count}</td>
                    <td className="px-3 py-2 text-[var(--text-primary)]">{cluster.size}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>

        <section className={card}>
          <h2 className="text-sm font-semibold text-[var(--text-primary)] mb-3">Cluster Detail</h2>
          {selectedCluster ? (
            <div className="space-y-3 text-sm">
              <p className="font-mono text-xs text-[var(--text-secondary)] break-all">{selectedCluster.key}</p>
              <div className="flex flex-wrap gap-2">
                <Badge text={`domain: ${selectedCluster.domain_id || '-'}`} />
                <Badge text={`source: ${[selectedCluster.source_kind, selectedCluster.source_name].filter(Boolean).join('/') || '-'}`} />
                <Badge text={`failure: ${selectedCluster.failure_type || '-'}`} />
              </div>
              <p className="text-[var(--text-primary)]">{selectedCluster.sample_message || '暂无样本错误'}</p>
              <div className="flex flex-wrap gap-2">
                {selectedCluster.candidate_ids?.map((id) => (
                  <span key={id} className="px-2 py-1 rounded-full bg-[var(--bg-secondary)] text-xs font-mono text-[var(--text-secondary)]">{id.slice(0, 18)}</span>
                ))}
              </div>
            </div>
          ) : (
            <p className="text-sm text-[var(--text-secondary)]">选择聚类后查看详情。</p>
          )}
        </section>
      </div>

      <section className={card}>
        <div className="flex flex-wrap items-center justify-between gap-3 mb-3">
          <div className="flex items-center gap-2">
            <Layers3 size={16} className="text-[var(--accent-600)]" />
            <h2 className="text-sm font-semibold text-[var(--text-primary)]">Workbench Advanced Ops</h2>
          </div>
          <div className="flex flex-wrap gap-2">
            <button className={button} onClick={previewGroupingRules}>
              <GitBranch size={14} />
              Grouping preview
            </button>
            <button className={button} onClick={saveToolGroupingRule}>
              <GitBranch size={14} />
              Save tool rule
            </button>
            <button className={button} onClick={deleteToolGroupingRule}>
              <XCircle size={14} />
              Delete tool rule
            </button>
            <button className={button} onClick={planReplayFanout}>
              <Play size={14} />
              Replay fanout
            </button>
            <button className={button} onClick={compareVersionDiff}>
              <RotateCcw size={14} />
              Version diff
            </button>
          </div>
        </div>
        <div className="grid grid-cols-1 xl:grid-cols-3 gap-3">
          <WorkbenchResult
            title="Grouping Preview"
            empty="尚未预览 grouping rules"
            content={groupingPreview ? `rules=${groupingRules.map((rule) => rule.id).join(', ') || 'default'}\nclusters=${groupingPreview.clusters.length}\nrule_hits=${JSON.stringify(groupingPreview.rule_hits, null, 2)}` : ''}
          />
          <WorkbenchResult
            title="Replay Fanout"
            empty="尚未生成 replay fanout"
            content={fanoutPlan ? `selected=${fanoutPlan.selected_ids.length}/${fanoutPlan.total}\ntruncated=${fanoutPlan.truncated}\nremaining=${fanoutPlan.remaining}\nids=${fanoutPlan.selected_ids.join(', ')}` : ''}
          />
          <WorkbenchResult
            title="Version Diff"
            empty="尚未比较版本矩阵"
            content={versionDiff ? `regressed=${versionDiff.regressed_case_ids.join(', ') || '-'}\nrecovered=${versionDiff.recovered_case_ids.join(', ') || '-'}\nnew_failure=${versionDiff.new_failure_case_ids.join(', ') || '-'}` : ''}
          />
        </div>
      </section>

      <div className="grid grid-cols-1 gap-5">
        <section className={card}>
          <div className="flex items-center gap-2 mb-3 overflow-x-auto">
            <button
              className={`${tabBase} ${activeTab === 'replay' ? tabActive : tabIdle}`}
              onClick={() => setActiveTab('replay')}
            >
              <Play size={14} />
              Replay
            </button>
            <button
              className={`${tabBase} ${activeTab === 'eval' ? tabActive : tabIdle}`}
              onClick={() => setActiveTab('eval')}
            >
              <RotateCcw size={14} />
              Batch Eval
            </button>
            <button
              className={`${tabBase} ${activeTab === 'report' ? tabActive : tabIdle}`}
              onClick={() => setActiveTab('report')}
            >
              <FileText size={14} />
              Weekly
            </button>
            <button
              className={`${tabBase} ${activeTab === 'distribution' ? tabActive : tabIdle}`}
              onClick={() => setActiveTab('distribution')}
            >
              <BarChart3 size={14} />
              失败分布
            </button>
          </div>

          {activeTab === 'replay' && (
            <div>
              <ListEmpty show={replays.length === 0} text="暂无 replay job — 选中聚类后点 Replay 入队" />
              {replays.slice(0, 8).map((job) => (
                <ReplayRow key={job.id} job={job} running={runningReplayId === job.id} onRun={() => runReplay(job)} />
              ))}
            </div>
          )}
          {activeTab === 'eval' && (
            <div>
              <div className="flex items-center justify-between mb-3">
                <p className="text-xs text-[var(--text-secondary)]">手动触发离线 eval,可指定 cases_dir</p>
                <button onClick={runBatchEval} className={button}><RotateCcw size={14} />运行</button>
              </div>
              <input
                className="mb-3 w-full rounded-[10px] border border-[var(--border-color)] bg-[var(--bg-primary)] px-3 py-2 text-sm text-[var(--text-primary)] placeholder:text-[var(--text-tertiary)]"
                value={casesDir}
                onChange={(event) => setCasesDir(event.target.value)}
                placeholder="可选 cases_dir,例如 ./testdata/golden"
              />
              <ListEmpty show={evals.length === 0} text="尚未运行 batch eval — 点上方运行按钮开始" />
              {evals.slice(0, 8).map((run) => (
                <BatchEvalRunRow key={run.id} run={run} />
              ))}
            </div>
          )}
          {activeTab === 'report' && (
            <div>
              <div className="flex items-center justify-between mb-3">
                <p className="text-xs text-[var(--text-secondary)]">每周一手动生成 markdown 周报</p>
                <button onClick={generateReport} className={button}><FileText size={14} />生成本周</button>
              </div>
              <ListEmpty show={reports.length === 0} text="暂无周报 — 点击「生成本周」产出 markdown" />
              {reports.slice(0, 5).map((report) => (
                <Row key={report.id} title={report.id} meta={report.title || 'Quality Workbench Weekly Report'} />
              ))}
            </div>
          )}
          {activeTab === 'distribution' && (
            <div>
              {snapshot && Object.keys(snapshot.failure_type_counts ?? {}).length > 0 ? (
                <FailureDistribution counts={snapshot.failure_type_counts ?? {}} />
              ) : (
                <p className="py-8 text-center text-sm text-[var(--text-secondary)]">尚无失败数据 — 等运行时产生 candidates 后展示分布</p>
              )}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

function Metric({ title, value, icon, accent }: { title: string; value: number; icon: React.ReactNode; accent?: boolean }) {
  return (
    <div className={accent ? `${card} ring-1 ring-[var(--accent-border)]` : card}>
      <div className="flex items-center justify-between">
        <p className="text-xs text-[var(--text-secondary)]">{title}</p>
        <span className={accent ? 'text-[var(--accent-700)]' : 'text-[var(--accent-600)]'}>{icon}</span>
      </div>
      <p className={`mt-2 text-2xl font-semibold ${accent ? 'text-[var(--accent-700)]' : 'text-[var(--text-primary)]'}`}>{value}</p>
    </div>
  );
}

function HeroBand({ openClusters, openCandidates, loading }: { openClusters: number; openCandidates: number; loading: boolean }) {
  const total = openClusters + openCandidates;
  if (loading) {
    return (
      <div className={heroCard}>
        <p className="text-sm text-[var(--text-secondary)]">加载中,正在汇总本周质量信号...</p>
      </div>
    );
  }
  if (total === 0) {
    return (
      <div className={heroCard}>
        <h2 className="text-base font-semibold text-[var(--text-primary)] font-display">质量基线稳定</h2>
        <p className="mt-1 text-sm text-[var(--text-secondary)]">当前没有待处理的失败聚类或候选。运行时若产生新失败,会自动出现在下面的 Open Clusters 表里。</p>
      </div>
    );
  }
  return (
    <div className={heroCard}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-base font-semibold text-[var(--accent-700)] font-display">需要关注</h2>
          <p className="mt-1 text-sm text-[var(--text-primary)]">
            <span className="font-mono font-semibold">{openCandidates}</span> 条待审核候选,<span className="font-mono font-semibold">{openClusters}</span> 个失败聚类待处理。
          </p>
          <p className="mt-1 text-xs text-[var(--text-secondary)]">优先处理 size 最大的聚类(下方表格首行),一次 Replay 即可批量复跑同类失败。</p>
        </div>
      </div>
    </div>
  );
}

function FailureDistribution({ counts }: { counts: Record<string, number> }) {
  const entries = Object.entries(counts).sort((a, b) => b[1] - a[1]);
  const max = entries.length > 0 ? entries[0][1] : 1;
  return (
    <div className="space-y-2">
      {entries.map(([key, value]) => {
        const pct = Math.max(4, Math.round((value / max) * 100));
        return (
          <div key={key}>
            <div className="flex items-center justify-between mb-1">
              <span className="text-xs font-mono text-[var(--text-primary)]">{key}</span>
              <span className="text-xs text-[var(--text-secondary)]">{value}</span>
            </div>
            <div className="h-2 rounded-full bg-[var(--bg-secondary)] overflow-hidden">
              <div className="h-full bg-[var(--accent-500)]" style={{ width: `${pct}%` }} />
            </div>
          </div>
        );
      })}
    </div>
  );
}

function BatchEvalRunRow({ run }: { run: BatchEvalRun }) {
  const runnerInfo = batchEvalRunnerInfo(run);
  const judgeVerdict = batchEvalJudgeVerdict(run);
  const gateMetrics = batchEvalGateMetrics(run);
  const shadowMetrics = batchEvalShadowMetrics(run);
  const shadowResults = run.shadow_results ?? [];
  const domainRegressions = batchEvalDomainRegressions(run);
  const summary = run.summary;

  return (
    <div className="py-3 border-t border-[var(--border-color)] first:border-t-0">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-sm text-[var(--text-primary)] font-mono truncate">{run.id}</p>
          <p className="text-xs text-[var(--text-secondary)] truncate">
            {run.kind} · {run.status} · {formatAttribution(run)} · pass {summary?.passed ?? 0} / fail {summary?.failed ?? 0} / unknown {summary?.unknown ?? 0}
          </p>
        </div>
        <EvidenceBadge level={runnerInfo?.evidence_level} />
      </div>
      <RunnerEvidenceLine runnerInfo={runnerInfo} />
      <div className="mt-3 grid grid-cols-1 lg:grid-cols-3 gap-2">
        <EvidenceCard title="Judge">
          <JudgeEvidence verdict={judgeVerdict} metrics={gateMetrics} />
        </EvidenceCard>
        <EvidenceCard title="Shadow">
          <ShadowEvidence metrics={shadowMetrics} results={shadowResults} kind={run.kind} />
        </EvidenceCard>
        <EvidenceCard title="Domain Regression">
          <DomainRegressionEvidence items={domainRegressions} domainId={run.domain_id} />
        </EvidenceCard>
      </div>
      {summary?.reasons?.length ? (
        <p className="mt-2 text-xs text-[var(--text-secondary)] truncate">reasons: {summary.reasons.slice(0, 3).join(' · ')}</p>
      ) : null}
    </div>
  );
}

function EvidenceCard({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="rounded-lg border border-[var(--border-color)] bg-[var(--bg-primary)] px-3 py-2">
      <p className="mb-1 text-[11px] font-medium uppercase tracking-wide text-[var(--text-secondary)]">{title}</p>
      {children}
    </div>
  );
}

function RunnerEvidenceLine({ runnerInfo }: { runnerInfo?: RunnerInfo }) {
  const level = runnerInfo?.evidence_level;
  const name = runnerInfo?.name || 'runner 未返回';
  const version = runnerInfo?.version ? ` v${runnerInfo.version}` : '';
  return (
    <div className="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
      <span className="text-[var(--text-secondary)]">runner</span>
      <span className="font-mono text-[var(--text-primary)]">{name}{version}</span>
      <EvidenceBadge level={level} />
      <span className={evidenceMessageClass(level)}>{evidenceMessage(level)}</span>
    </div>
  );
}

function EvidenceBadge({ level }: { level?: string }) {
  const normalized = normalizeEvidenceLevel(level);
  const cls = normalized === 'static_schema' || normalized === 'unknown'
    ? 'border-red-200 bg-red-50 text-red-700'
    : normalized === 'real_runner' || normalized === 'production_shadow' || normalized === 'human_verified'
      ? 'border-emerald-200 bg-emerald-50 text-emerald-700'
      : 'border-amber-200 bg-amber-50 text-amber-700';
  return <span className={`inline-flex items-center rounded-full border px-2 py-0.5 text-[11px] font-mono ${cls}`}>{normalized}</span>;
}

function JudgeEvidence({ verdict, metrics }: { verdict?: QualityEvaluationVerdict; metrics?: GateMetrics }) {
  if (!verdict && !metrics) {
    return <p className="text-xs text-[var(--text-secondary)]">未返回 judge verdict 或 gate metrics。</p>;
  }
  return (
    <div className="space-y-1 text-xs text-[var(--text-secondary)]">
      {verdict ? (
        <p>
          score <span className="font-mono text-[var(--text-primary)]">{verdict.score ?? '-'}</span>
          {verdict.failure_type ? <> · failure <span className="font-mono text-[var(--text-primary)]">{verdict.failure_type}</span></> : null}
          {typeof verdict.should_optimize === 'boolean' ? <> · optimize <span className="font-mono text-[var(--text-primary)]">{String(verdict.should_optimize)}</span></> : null}
        </p>
      ) : null}
      {verdict?.verdict ? <p className="truncate">{verdict.verdict}</p> : null}
      {metrics ? (
        <p>
          semantic <span className="font-mono text-[var(--text-primary)]">{formatOptionalNumber(metrics.semantic_score)}</span>
          {metrics.judge_missing ? <span className="ml-1 text-red-700">judge_missing</span> : null}
          {metrics.judge_required_domain ? <> · domain <span className="font-mono text-[var(--text-primary)]">{metrics.judge_required_domain}</span></> : null}
        </p>
      ) : null}
    </div>
  );
}

function ShadowEvidence({ metrics, results, kind }: { metrics: ShadowEvalMetrics[]; results: ShadowEvalResult[]; kind: string }) {
  if (metrics.length === 0 && results.length === 0) {
    return <p className="text-xs text-[var(--text-secondary)]">{kind === 'shadow' ? 'shadow run 未返回 metrics。' : '未返回 shadow metrics。'}</p>;
  }
  if (metrics.length > 0) {
    return (
      <div className="space-y-1 text-xs text-[var(--text-secondary)]">
        {metrics.slice(0, 2).map((item, index) => (
          <p key={`${item.domain_id || 'shadow'}-${index}`} className="truncate">
            <span className="font-mono text-[var(--text-primary)]">{item.domain_id || 'domain:-'}</span>
            {' '}samples {item.sample_count ?? '-'} · pass {formatOptionalPercent(item.pass_rate)} · semantic {formatOptionalNumber(item.avg_semantic_score)}
          </p>
        ))}
      </div>
    );
  }
  const passed = results.filter((item) => item.passed).length;
  const avgScore = averageShadowScore(results);
  return (
    <p className="text-xs text-[var(--text-secondary)]">
      results {results.length} · pass {passed}/{results.length} · semantic {formatOptionalNumber(avgScore)}
    </p>
  );
}

function DomainRegressionEvidence({ items, domainId }: { items: DomainRegressionStatus[]; domainId?: string }) {
  if (items.length === 0) {
    return <p className="text-xs text-[var(--text-secondary)]">{domainId ? `${domainId}: 未返回 regression status。` : '未返回 domain regression status。'}</p>;
  }
  return (
    <div className="space-y-1 text-xs text-[var(--text-secondary)]">
      {items.slice(0, 3).map((item, index) => (
        <p key={`${item.domain_id || 'domain'}-${index}`} className="truncate">
          <span className={domainStatusClass(item.status)}>{item.status || 'unknown'}</span>
          {' '}<span className="font-mono text-[var(--text-primary)]">{item.domain_id || '-'}</span>
          {' '}cases {item.active_cases ?? '-'} · safety {item.safety_failures ?? '-'} · evidence {normalizeEvidenceLevel(item.evidence_level)}
        </p>
      ))}
    </div>
  );
}

function batchEvalRunnerInfo(run: BatchEvalRun): RunnerInfo | undefined {
  const evidenceLevel = run.runner_info?.evidence_level || run.evidence_level || run.summary?.evidence_level;
  if (!run.runner_info && !evidenceLevel) return undefined;
  return { ...run.runner_info, evidence_level: evidenceLevel };
}

function batchEvalJudgeVerdict(run: BatchEvalRun): QualityEvaluationVerdict | undefined {
  return run.judge_verdict || run.summary?.judge_verdict;
}

function batchEvalGateMetrics(run: BatchEvalRun): GateMetrics | undefined {
  return run.gate_metrics || run.summary?.gate_metrics;
}

function batchEvalShadowMetrics(run: BatchEvalRun): ShadowEvalMetrics[] {
  const value = run.shadow_metrics || run.summary?.shadow_metrics || run.diff?.shadow_metrics;
  if (!value) return [];
  return Array.isArray(value) ? value : [value];
}

function batchEvalDomainRegressions(run: BatchEvalRun): DomainRegressionStatus[] {
  const value = run.domain_regressions || run.domain_regression || run.summary?.domain_regressions || run.summary?.domain_regression || run.diff?.domain_regressions;
  if (!value) return [];
  return Array.isArray(value) ? value : [value];
}

function normalizeEvidenceLevel(level?: string) {
  const trimmed = level?.trim();
  return trimmed || 'unknown';
}

function evidenceMessage(level?: string) {
  const normalized = normalizeEvidenceLevel(level);
  if (normalized === 'static_schema') return '仅静态结构检查，不能授权优化或上线。';
  if (normalized === 'unknown') return '未返回 runner 证据，不能作为审批依据。';
  if (normalized === 'real_runner' || normalized === 'production_shadow' || normalized === 'human_verified') return '真实执行证据，仍需审批复核。';
  return '有限证据，需结合 judge、shadow 和回放结果复核。';
}

function evidenceMessageClass(level?: string) {
  const normalized = normalizeEvidenceLevel(level);
  if (normalized === 'static_schema' || normalized === 'unknown') return 'text-red-700';
  if (normalized === 'real_runner' || normalized === 'production_shadow' || normalized === 'human_verified') return 'text-emerald-700';
  return 'text-amber-700';
}

function domainStatusClass(status?: string) {
  if (status === 'pass') return 'font-mono text-emerald-700';
  if (status === 'fail') return 'font-mono text-red-700';
  return 'font-mono text-amber-700';
}

function formatOptionalNumber(value?: number) {
  return typeof value === 'number' && Number.isFinite(value) ? value.toFixed(2) : '-';
}

function formatOptionalPercent(value?: number) {
  return typeof value === 'number' && Number.isFinite(value) ? `${(value * 100).toFixed(1)}%` : '-';
}

function averageShadowScore(results: ShadowEvalResult[]) {
  const scores = results.map((item) => item.judge_verdict?.score).filter((score): score is number => typeof score === 'number');
  if (scores.length === 0) return undefined;
  return scores.reduce((sum, score) => sum + score, 0) / scores.length;
}

function FilterInput({ label, value, placeholder, onChange }: { label: string; value: string; placeholder: string; onChange: (value: string) => void }) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs font-medium text-[var(--text-secondary)]">{label}</span>
      <input
        className="w-full rounded-[10px] border border-[var(--border-color)] bg-[var(--bg-primary)] px-3 py-2 text-sm text-[var(--text-primary)] placeholder:text-[var(--text-tertiary)]"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
      />
    </label>
  );
}

function FilterSelect({ label, value, options, onChange }: { label: string; value: string; options: Array<[string, string]>; onChange: (value: string) => void }) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs font-medium text-[var(--text-secondary)]">{label}</span>
      <select
        className="w-full rounded-[10px] border border-[var(--border-color)] bg-[var(--bg-primary)] px-3 py-2 text-sm text-[var(--text-primary)]"
        value={value}
        onChange={(event) => onChange(event.target.value)}
      >
        {options.map(([optionValue, labelText]) => (
          <option key={optionValue || 'all'} value={optionValue}>{labelText}</option>
        ))}
      </select>
    </label>
  );
}

function Badge({ text }: { text: string }) {
  return <span className="px-2 py-1 rounded-full bg-[var(--bg-secondary)] text-xs font-mono text-[var(--text-secondary)]">{text}</span>;
}

function Row({ title, meta }: { title: string; meta: string }) {
  return (
    <div className="py-2 border-t border-[var(--border-color)] first:border-t-0">
      <p className="text-sm text-[var(--text-primary)] font-mono truncate">{title}</p>
      <p className="text-xs text-[var(--text-secondary)] truncate">{meta}</p>
    </div>
  );
}

function WorkbenchResult({ title, empty, content }: { title: string; empty: string; content: string }) {
  return (
    <div className="rounded-lg border border-[var(--border-color)] p-3">
      <p className="mb-2 text-xs font-medium text-[var(--text-secondary)]">{title}</p>
      {content ? (
        <pre className="max-h-36 overflow-auto rounded-md bg-[var(--bg-secondary)] px-2 py-2 text-xs text-[var(--text-primary)] whitespace-pre-wrap">{content}</pre>
      ) : (
        <p className="text-sm text-[var(--text-secondary)]">{empty}</p>
      )}
    </div>
  );
}

function ReplayRow({ job, running, onRun }: { job: ReplayJob; running: boolean; onRun: () => void }) {
  const canRun = job.status === 'queued' || job.status === 'failed';
  const runnerInfo = job.result?.runner_info;
  return (
    <div className="py-3 border-t border-[var(--border-color)] first:border-t-0">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-sm text-[var(--text-primary)] font-mono truncate">{job.id}</p>
          <p className="text-xs text-[var(--text-secondary)] truncate">{job.kind} · {job.status} · {formatAttribution(job)} · attempt {job.attempt}/{job.max_attempt}</p>
          <p className="text-xs text-[var(--text-secondary)] truncate">{job.target_ids?.join(', ') || '-'}</p>
        </div>
        <button className={button} onClick={onRun} disabled={!canRun || running}>
          <Play size={14} />
          {running ? '运行中' : 'Run'}
        </button>
      </div>
      {runnerInfo ? <RunnerEvidenceLine runnerInfo={runnerInfo} /> : null}
      {job.error ? (
        <p className="mt-2 rounded-md bg-red-50 px-2 py-1 text-xs text-red-700">{job.error}</p>
      ) : null}
      {job.result ? (
        <pre className="mt-2 max-h-28 overflow-auto rounded-md bg-[var(--bg-secondary)] px-2 py-1 text-xs text-[var(--text-secondary)] whitespace-pre-wrap">
          {JSON.stringify(job.result, null, 2)}
        </pre>
      ) : null}
    </div>
  );
}

function formatAttribution(item: Pick<ReplayJob | BatchEvalRun, 'domain_id' | 'source_kind' | 'source_name'>) {
  const source = [item.source_kind, item.source_name].filter(Boolean).join('/');
  return [item.domain_id, source].filter(Boolean).join(' · ') || 'generic';
}

function ListEmpty({ show, text }: { show: boolean; text: string }) {
  if (!show) return null;
  return <p className="py-6 text-center text-sm text-[var(--text-secondary)]">{text}</p>;
}
