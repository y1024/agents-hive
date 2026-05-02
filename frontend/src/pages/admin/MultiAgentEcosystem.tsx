import { useCallback, useEffect, useMemo, useState } from 'react';
import { Bot, GitBranch, PlugZap, RefreshCcw, ShieldCheck } from 'lucide-react';
import { useNodeClient } from '../../hooks/useNodeClient';
import { useToastStore } from '../../store/toast';
import type { AgentInfo, RemoteAgentConfig, RemoteAgentHealth, RuntimeConfig } from '../../types/api';

const card = 'rounded-2xl border border-[var(--border-color)] bg-[var(--bg-card)] p-4 shadow-sm';
const button = 'inline-flex items-center justify-center gap-2 px-3 py-2 rounded-[10px] border border-[var(--border-color)] text-sm text-[var(--text-primary)] hover:bg-[var(--bg-secondary)] disabled:opacity-50 disabled:cursor-not-allowed transition-colors duration-150';

export function MultiAgentEcosystem() {
  const client = useNodeClient();
  const addToast = useToastStore((s) => s.addToast);
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [remoteAgents, setRemoteAgents] = useState<RemoteAgentConfig[]>([]);
  const [health, setHealth] = useState<Record<string, RemoteAgentHealth>>({});
  const [policy, setPolicy] = useState<RuntimeConfig | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [agentList, remoteList, remoteHealth, runtime] = await Promise.all([
        client.listAgents(),
        client.listRemoteAgents().catch(() => [] as RemoteAgentConfig[]),
        client.healthCheckRemoteAgents().catch(() => ({} as Record<string, RemoteAgentHealth>)),
        client.getRuntimeConfig().catch(() => null),
      ]);
      setAgents(agentList ?? []);
      setRemoteAgents(remoteList ?? []);
      setHealth(remoteHealth ?? {});
      setPolicy(runtime);
    } catch (e: unknown) {
      addToast('error', e instanceof Error ? e.message : '加载 multi-agent 生态失败');
    } finally {
      setLoading(false);
    }
  }, [client, addToast]);

  useEffect(() => { load(); }, [load]);

  const toolPolicy = useMemo(() => {
    const rules = policy?.hitl?.permission_rules ?? [];
    const spawn = rules.find((rule) => rule.tool_name === 'spawn_agent')?.action ?? 'ask';
    const read = rules.find((rule) => rule.tool_name === 'read_file')?.action ?? 'allow';
    const bash = rules.find((rule) => rule.tool_name === 'bash')?.action ?? 'ask';
    return { spawn, read, bash };
  }, [policy]);

  return (
    <div className="p-6 max-w-7xl mx-auto space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold text-[var(--text-primary)] font-display">Multi-agent / ACP 生态</h1>
          <p className="mt-1 text-sm text-[var(--text-secondary)]">
            查看本地 agent、远程 ACP agent、运行时权限和委派边界。普通读取放行，写入/删除/终端等高风险操作继续受控。
          </p>
        </div>
        <button onClick={load} className={button} disabled={loading}>
          <RefreshCcw size={14} />
          刷新
        </button>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
        <Metric title="Local Agents" value={agents.length} icon={<Bot size={17} />} />
        <Metric title="Remote ACP" value={remoteAgents.length} icon={<PlugZap size={17} />} />
        <Metric title="Running Remote" value={Object.values(health).filter((h) => String(h.status).includes('running') || h.status === 1).length} icon={<GitBranch size={17} />} />
        <Metric title="spawn_agent Policy" value={toolPolicy.spawn} icon={<ShieldCheck size={17} />} />
      </div>

      <div className="grid grid-cols-1 xl:grid-cols-2 gap-5">
        <section className={card}>
          <h2 className="text-sm font-semibold text-[var(--text-primary)] mb-3">本地 Agent</h2>
          <div className="space-y-2">
            {agents.length === 0 ? (
              <div className="py-8 text-center space-y-2">
                <p className="text-sm font-medium text-[var(--text-primary)]">{loading ? '加载中...' : '尚未注册本地 agent'}</p>
                {!loading && (
                  <p className="text-xs text-[var(--text-secondary)]">在 `internal/agents/` 下定义 agent + skills 后,启动时会自动出现在这里。</p>
                )}
              </div>
            ) : agents.map((agent) => (
              <div key={agent.id} className="rounded-lg border border-[var(--border-color)] p-3">
                <div className="flex items-center justify-between gap-3">
                  <p className="font-medium text-sm text-[var(--text-primary)]">{agent.name || agent.id}</p>
                  {agent.dynamic && <span className="px-2 py-0.5 rounded-full text-[11px] bg-[var(--accent-100)] text-[var(--accent-700)]">dynamic</span>}
                </div>
                <p className="mt-1 text-xs text-[var(--text-secondary)]">{agent.description || '无描述'}</p>
                <p className="mt-2 text-xs text-[var(--text-secondary)]">skills: {(agent.skills ?? []).join(', ') || '-'}</p>
              </div>
            ))}
          </div>
        </section>

        <section className={card}>
          <h2 className="text-sm font-semibold text-[var(--text-primary)] mb-3">远程 ACP Agent</h2>
          <div className="space-y-2">
            {remoteAgents.length === 0 ? (
              <div className="py-8 text-center space-y-2">
                <p className="text-sm font-medium text-[var(--text-primary)]">尚未配置远程 ACP agent</p>
                <p className="text-xs text-[var(--text-secondary)]">通过 ACP 协议(stdio / http transport)连接外部 agent,在 admin 配置中加入即可。当前所有写入与终端操作仍走本地 HITL 权限策略。</p>
              </div>
            ) : remoteAgents.map((agent) => (
              <div key={agent.name} className="rounded-lg border border-[var(--border-color)] p-3">
                <div className="flex items-center justify-between gap-3">
                  <p className="font-medium text-sm text-[var(--text-primary)]">{agent.name}</p>
                  <span className="px-2 py-0.5 rounded-full text-[11px] bg-[var(--bg-secondary)] text-[var(--text-secondary)]">{String(health[agent.name]?.status ?? 'unknown')}</span>
                </div>
                <p className="mt-1 text-xs text-[var(--text-secondary)]">{agent.transport} · {agent.enabled ? 'enabled' : 'disabled'}</p>
                <p className="mt-2 text-xs text-[var(--text-secondary)] truncate">{agent.command || agent.url || '-'}</p>
              </div>
            ))}
          </div>
        </section>
      </div>

      <section className={card}>
        <h2 className="text-sm font-semibold text-[var(--text-primary)] mb-3">权限边界</h2>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
          <Policy name="read_file" action={toolPolicy.read} hint="读取类能力应默认放行，减少无效审批。" />
          <Policy name="spawn_agent" action={toolPolicy.spawn} hint="委派会形成 trace；是否审批取决于运行时策略。" />
          <Policy name="bash/write/delete" action={toolPolicy.bash} hint="删除、覆盖、终端写入等危险操作才需要审批。" />
        </div>
      </section>
    </div>
  );
}

function Metric({ title, value, icon }: { title: string; value: number | string; icon: React.ReactNode }) {
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

function Policy({ name, action, hint }: { name: string; action: string; hint: string }) {
  return (
    <div className="rounded-lg border border-[var(--border-color)] p-3">
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm font-mono text-[var(--text-primary)]">{name}</p>
        <span className="px-2 py-0.5 rounded-full text-[11px] bg-[var(--bg-secondary)] text-[var(--text-secondary)]">{action}</span>
      </div>
      <p className="mt-2 text-xs text-[var(--text-secondary)]">{hint}</p>
    </div>
  );
}
