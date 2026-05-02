import { useCallback, useEffect, useMemo, useState } from 'react';
import { BarChart3, FileText, GitBranch, Layers3, Play, RefreshCcw, RotateCcw, XCircle } from 'lucide-react';
import { useNodeClient } from '../../../hooks/useNodeClient';
import { useToastStore } from '../../../store/toast';
import type { BatchEvalRun, GroupingRule, GroupingRulePreview, QualityDashboardSnapshot, QualityReport, QualityWorkbenchCluster, ReplayFanoutPlan, ReplayJob, VersionDiff } from '../../../types/api';

const card = 'rounded-2xl border border-[var(--border-color)] bg-[var(--bg-card)] p-4 shadow-sm';
const heroCard = 'rounded-2xl border border-[var(--accent-border)] bg-[var(--accent-subtle)] p-5';
const button = 'inline-flex items-center justify-center gap-2 px-3 py-2 rounded-[10px] border border-[var(--border-color)] text-sm text-[var(--text-primary)] hover:bg-[var(--bg-secondary)] disabled:opacity-50 disabled:cursor-not-allowed transition-colors duration-150';
const primaryButton = 'inline-flex items-center justify-center gap-2 px-3 py-2 rounded-[10px] bg-[var(--accent-600)] text-white text-sm hover:bg-[var(--accent-700)] disabled:opacity-50 disabled:cursor-not-allowed transition-colors duration-150';
const tabBase = 'inline-flex items-center gap-2 px-3 py-1.5 rounded-full text-sm transition-colors duration-150';
const tabActive = 'bg-[var(--accent-600)] text-white';
const tabIdle = 'text-[var(--text-secondary)] hover:bg-[var(--bg-secondary)]';

type WorkbenchTab = 'replay' | 'eval' | 'report' | 'distribution';

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

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [clusterRes, replayRes, evalRes, reportRes, snap, rulesRes] = await Promise.all([
        client.adminListQualityWorkbenchClusters({ page: 1, size: 100 }),
        client.adminListReplayJobs({ page: 1, size: 50 }),
        client.adminListBatchEvals(),
        client.adminListQualityReports(),
        client.adminGetQualityDashboardSnapshot(),
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
  }, [client, addToast]);

  useEffect(() => { load(); }, [load]);

  const openCandidateCount = useMemo(() => {
    const counts = snapshot?.candidate_status_counts ?? {};
    return (counts.new ?? 0) + (counts.reviewing ?? 0);
  }, [snapshot]);

  const createReplay = async (cluster: QualityWorkbenchCluster | null) => {
    if (!cluster) return;
    try {
      await client.adminCreateReplayJobs({ kind: 'cluster', target_ids: [cluster.id], max_attempt: 1 });
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
      await client.adminCreateBatchEval({ mode: 'manual', ...(trimmedCasesDir ? { cases_dir: trimmedCasesDir } : {}) });
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
                  <th className="px-3 py-2 text-left font-medium">Tool/Skill</th>
                  <th className="px-3 py-2 text-left font-medium">Open</th>
                  <th className="px-3 py-2 text-left font-medium">Size</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[var(--border-color)]">
                {clusters.length === 0 ? (
                  <tr>
                    <td colSpan={5} className="px-3 py-10 text-center">
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
                <Row key={run.id} title={run.id} meta={`${run.status} · pass ${run.summary?.passed ?? 0} / fail ${run.summary?.failed ?? 0} / unknown ${run.summary?.unknown ?? 0}`} />
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
  return (
    <div className="py-3 border-t border-[var(--border-color)] first:border-t-0">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-sm text-[var(--text-primary)] font-mono truncate">{job.id}</p>
          <p className="text-xs text-[var(--text-secondary)] truncate">{job.kind} · {job.status} · attempt {job.attempt}/{job.max_attempt}</p>
          <p className="text-xs text-[var(--text-secondary)] truncate">{job.target_ids?.join(', ') || '-'}</p>
        </div>
        <button className={button} onClick={onRun} disabled={!canRun || running}>
          <Play size={14} />
          {running ? '运行中' : 'Run'}
        </button>
      </div>
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

function ListEmpty({ show, text }: { show: boolean; text: string }) {
  if (!show) return null;
  return <p className="py-6 text-center text-sm text-[var(--text-secondary)]">{text}</p>;
}
