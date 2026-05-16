import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { Activity, Users, Zap, Bot, AlertTriangle, ArrowRight } from 'lucide-react';
import { useNodeClient } from '../hooks/useNodeClient';
import { useToastStore } from '../store/toast';
import { GradientCard } from '../components/common/GradientCard';
import type { Health, AgentInfo, SkillMetadata } from '../types/api';

export function Dashboard() {
  const { t } = useTranslation();
  const client = useNodeClient();
  const navigate = useNavigate();
  const addToast = useToastStore((s) => s.addToast);
  const [health, setHealth] = useState<Health | null>(null);
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [skills, setSkills] = useState<SkillMetadata[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);

  useEffect(() => {
    const load = async () => {
      setLoading(true);
      let hasError = false;
      try {
        const [h, a, s] = await Promise.all([
          client.health().catch(() => { hasError = true; return null; }),
          client.listAgents().catch(() => { hasError = true; return [] as AgentInfo[]; }),
          client.listSkills().catch(() => { hasError = true; return [] as SkillMetadata[]; }),
        ]);
        setHealth(h);
        setAgents(a ?? []);
        setSkills(s ?? []);
        if (hasError) {
          setLoadError(true);
          addToast('error', t('dashboard.partialLoadError', '部分数据加载失败，请检查后端服务状态'));
        }
      } finally {
        setLoading(false);
      }
    };
    load();
  }, [client, addToast, t]);

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-[var(--text-secondary)] text-sm animate-pulse">{t('dashboard.loading')}</div>
      </div>
    );
  }

  return (
    <div className="p-6 max-w-6xl mx-auto">
      <h2 className="text-lg font-semibold text-[var(--text-primary)] mb-6 font-display">{t('dashboard.title')}</h2>

      {/* 加载失败警告 */}
      {loadError && (
        <div className="flex items-center gap-2 mb-4 p-3 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg text-red-600 dark:text-red-400 text-sm">
          <AlertTriangle className="w-4 h-4 flex-shrink-0" />
          <span>{t('dashboard.partialLoadWarning', '部分数据加载失败，显示的信息可能不完整')}</span>
        </div>
      )}

      {/* 状态概览：主状态 + 指标行 */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4 mb-8">
        {/* 系统状态 — 主卡片，横跨左侧 */}
        <div className="lg:row-span-2">
          <div className="h-full bg-[var(--bg-card)] border border-[var(--border-color)] rounded-2xl shadow-sm overflow-hidden p-5 flex flex-col justify-between">
            <div>
              <div className="flex items-center gap-2 text-xs text-[var(--text-secondary)] mb-3">
                <Activity className="w-3.5 h-3.5" />
                {t('dashboard.status')}
              </div>
              <div className="flex items-center gap-3">
                <span className={`w-3 h-3 rounded-full ${health ? 'bg-emerald-500 animate-pulse' : 'bg-red-500'}`} />
                <span className="text-2xl font-semibold text-[var(--text-primary)] font-display">
                  {health ? t('dashboard.operational') : t('dashboard.offline')}
                </span>
              </div>
            </div>
            <div className="mt-4 pt-4 border-t border-[var(--border-color)] text-xs text-[var(--text-secondary)]">
              {health ? t('dashboard.allSystemsNormal', '所有系统运行正常') : t('dashboard.checkBackend', '请检查后端服务')}
            </div>
          </div>
        </div>
        {/* 右侧三个指标 */}
        <GradientCard
          label={t('dashboard.activeSessions')}
          value={String(health?.active_sessions ?? 0)}
          accent="amber"
          icon={<Users className="w-3.5 h-3.5" />}
        />
        <GradientCard
          label={t('dashboard.registeredAgents')}
          value={String(agents.length)}
          accent="amber"
          icon={<Bot className="w-3.5 h-3.5" />}
        />
        <GradientCard
          label={t('dashboard.availableSkills')}
          value={String(skills.length)}
          accent="amber"
          icon={<Zap className="w-3.5 h-3.5" />}
        />
      </div>

      {/* Agent 列表 */}
      <section className="mb-8">
        <h3 className="text-base font-medium text-[var(--text-secondary)] mb-3">
          {t('dashboard.registeredAgents')}
        </h3>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
          {agents.map((agent) => (
            <div key={agent.id} className="bg-[var(--bg-card)] border border-[var(--border-color)] rounded-2xl shadow-sm p-5 transition-shadow">
              <div className="flex items-center gap-2.5 mb-1">
                <Bot className="w-4 h-4 text-[var(--accent-600)] dark:text-[var(--accent-300)]" />
                <span className="text-sm font-medium text-[var(--text-primary)]">{agent.name}</span>
              </div>
              <p className="text-xs text-[var(--text-secondary)] ml-6.5">{agent.description}</p>
              {agent.skills && agent.skills.length > 0 && (
                <div className="mt-2 ml-6.5 flex flex-wrap gap-1.5">
                  {agent.skills.map((sk) => (
                    <span key={sk} className="px-2 py-0.5 text-xs bg-[var(--accent-50)] dark:bg-[var(--accent-light)] text-[var(--accent-600)] dark:text-[var(--accent-300)] rounded-md">
                      {sk}
                    </span>
                  ))}
                </div>
              )}
            </div>
          ))}
          {agents.length === 0 && (
            <div className="col-span-2 text-center py-10">
              <Bot className="w-8 h-8 mx-auto mb-3 text-[var(--text-secondary)] opacity-40" />
              <p className="text-sm text-[var(--text-secondary)] mb-3">{t('dashboard.noAgents')}</p>
              <button
                onClick={() => navigate('/admin/agents')}
                className="inline-flex items-center gap-1.5 text-sm text-[var(--accent-600)] dark:text-[var(--accent-300)] hover:underline"
              >
                {t('dashboard.goToAgents', '查看 Agent 配置')}
                <ArrowRight className="w-3.5 h-3.5" />
              </button>
            </div>
          )}
        </div>
      </section>

      {/* Skill 列表 */}
      <section>
        <h3 className="text-base font-medium text-[var(--text-secondary)] mb-3">
          {t('dashboard.availableSkills')}
        </h3>
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
          {skills.map((skill) => (
            <div key={skill.name} className="bg-[var(--bg-card)] border border-[var(--border-color)] rounded-2xl shadow-sm p-5 transition-shadow">
              <div className="flex items-center gap-2 mb-1">
                <Zap className="w-3.5 h-3.5 text-[var(--accent-600)] dark:text-[var(--accent-300)]" />
                <span className="text-sm font-medium text-[var(--accent-600)] dark:text-[var(--accent-300)]">{skill.name}</span>
              </div>
              <p className="text-xs text-[var(--text-secondary)] mt-1 line-clamp-2">{skill.description}</p>
            </div>
          ))}
          {skills.length === 0 && (
            <div className="col-span-3 text-center py-10">
              <Zap className="w-8 h-8 mx-auto mb-3 text-[var(--text-secondary)] opacity-40" />
              <p className="text-sm text-[var(--text-secondary)] mb-3">{t('dashboard.noSkills')}</p>
              <button
                onClick={() => navigate('/admin/skills')}
                className="inline-flex items-center gap-1.5 text-sm text-[var(--accent-600)] dark:text-[var(--accent-300)] hover:underline"
              >
                {t('dashboard.goToSkills', '查看技能列表')}
                <ArrowRight className="w-3.5 h-3.5" />
              </button>
            </div>
          )}
        </div>
      </section>
    </div>
  );
}
