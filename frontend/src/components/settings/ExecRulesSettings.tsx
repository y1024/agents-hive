import { useEffect, useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { useNodeClient } from '../../hooks/useNodeClient';
import { useToastStore } from '../../store/toast';
import type { ExecRule, RuntimeConfig } from '../../types/api';

const DEFAULT_RULES: ExecRule[] = [
  { pattern: '^ls\\b', policy: 'allow', description: 'list files' },
  { pattern: '^cat\\b', policy: 'allow', description: 'read file' },
  { pattern: '^pwd$', policy: 'allow', description: 'print working directory' },
  { pattern: '^echo\\b', policy: 'allow', description: 'echo output' },
  { pattern: '^npm\\s+install', policy: 'ask', description: 'npm install needs approval' },
  { pattern: '^git\\s+push', policy: 'ask', description: 'git push needs approval' },
  { pattern: '^rm\\s+-rf\\s+', policy: 'ask', description: 'recursive delete needs approval' },
];

export function ExecRulesSettings() {
  const { t } = useTranslation();
  const client = useNodeClient();
  const addToast = useToastStore((s) => s.addToast);

  const [rules, setRules] = useState<ExecRule[]>([]);
  const [defaultPolicy, setDefaultPolicy] = useState<'allow' | 'ask' | 'deny'>('allow');
  const [permissionMode, setPermissionMode] = useState<'minimal' | 'strict'>('minimal');
  const [loading, setLoading] = useState(true);
  const [applying, setApplying] = useState(false);

  const loadConfig = useCallback(async () => {
    setLoading(true);
    try {
      const cfg: RuntimeConfig = await client.getRuntimeConfig();
      if (cfg.security?.exec_rules) {
        setRules(cfg.security.exec_rules);
      }
      setDefaultPolicy(cfg.security?.default_policy ?? 'allow');
      setPermissionMode(cfg.security?.permission_mode ?? 'minimal');
    } catch (e) {
      const msg = e instanceof Error ? e.message : t('runtimeConfig.loadFailed');
      addToast('error', msg);
    } finally {
      setLoading(false);
    }
  }, [client, addToast, t]);

  useEffect(() => {
    loadConfig();
  }, [loadConfig]);

  const handleApply = async () => {
    setApplying(true);
    try {
      await client.updateRuntimeConfig({
        security: {
          default_policy: defaultPolicy,
          permission_mode: permissionMode,
          exec_rules: rules,
        },
      });
      addToast('success', t('runtimeConfig.applySuccess'));
    } catch (e) {
      const msg = e instanceof Error ? e.message : t('runtimeConfig.applyFailed');
      addToast('error', msg);
    } finally {
      setApplying(false);
    }
  };

  const updateRule = (index: number, field: keyof ExecRule, value: string) => {
    setRules((prev) => prev.map((r, i) => (i === index ? { ...r, [field]: value } : r)));
  };

  const deleteRule = (index: number) => {
    setRules((prev) => prev.filter((_, i) => i !== index));
  };

  const addRule = () => {
    setRules((prev) => [...prev, { pattern: '', policy: 'ask', description: '' }]);
  };

  const resetDefaults = () => {
    setRules([...DEFAULT_RULES]);
    setDefaultPolicy('allow');
    setPermissionMode('minimal');
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12 text-[var(--text-secondary)]">
        {t('common.loading')}
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <SettingsSection
        title={t('runtimeConfig.execRules', 'Command Execution Rules')}
        action={
          <button
            onClick={resetDefaults}
            className="text-xs text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors"
          >
            {t('runtimeConfig.resetDefaults')}
          </button>
        }
      >
        <div className="p-5 space-y-4">
          <p className="text-xs text-[var(--text-secondary)] mb-3">
            {t('runtimeConfig.execRulesHint', 'Define regex patterns to allow, ask, or deny command execution. Rules are evaluated in order; first match wins.')}
          </p>

          <div className="flex items-center gap-3 py-2 px-3 bg-[var(--bg-primary)] rounded-lg border border-[var(--border-color)]">
            <span className="text-xs text-[var(--text-secondary)] flex-1">
              {t('runtimeConfig.permissionMode')}
            </span>
            <select
              aria-label={t('runtimeConfig.permissionMode')}
              value={permissionMode}
              onChange={(e) => setPermissionMode(e.target.value as 'minimal' | 'strict')}
              className="px-2 py-1 text-xs rounded-lg border border-[var(--border-color)] bg-[var(--bg-card)] text-[var(--text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-subtle)] focus:border-[var(--accent)]"
            >
              <option value="minimal">{t('runtimeConfig.permissionModeMinimal')}</option>
              <option value="strict">{t('runtimeConfig.permissionModeStrict')}</option>
            </select>
          </div>

          {/* 默认策略选择器 */}
          <div className="flex items-center gap-3 py-2 px-3 bg-[var(--bg-primary)] rounded-lg border border-[var(--border-color)]">
            <span className="text-xs text-[var(--text-secondary)] flex-1">
              {t('runtimeConfig.defaultPolicy', 'Default policy (when no rule matches)')}
            </span>
            <select
              aria-label={t('runtimeConfig.defaultPolicy', 'Default policy (when no rule matches)')}
              value={defaultPolicy}
              onChange={(e) => setDefaultPolicy(e.target.value as 'allow' | 'ask' | 'deny')}
              className="px-2 py-1 text-xs rounded-lg border border-[var(--border-color)] bg-[var(--bg-card)] text-[var(--text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-subtle)] focus:border-[var(--accent)]"
            >
              <option value="allow">{t('runtimeConfig.allow', 'Allow')}</option>
              <option value="ask">{t('runtimeConfig.ask', 'Ask')}</option>
              <option value="deny">{t('runtimeConfig.deny', 'Deny')}</option>
            </select>
          </div>

          <div className="grid grid-cols-[1fr_100px_1fr_40px] gap-2 text-xs font-medium text-[var(--text-secondary)] px-1">
            <span>{t('runtimeConfig.pattern', 'Pattern (regex)')}</span>
            <span>{t('runtimeConfig.action', 'Policy')}</span>
            <span>{t('runtimeConfig.description', 'Description')}</span>
            <span></span>
          </div>

          {rules.map((rule, i) => (
            <div key={i} className="grid grid-cols-[1fr_100px_1fr_40px] gap-2 items-center">
              <input
                type="text"
                value={rule.pattern}
                onChange={(e) => updateRule(i, 'pattern', e.target.value)}
                placeholder="^npm\s+install"
                className="px-2 py-1.5 text-sm rounded-lg border border-[var(--border-color)] bg-[var(--bg-primary)] text-[var(--text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-subtle)] focus:border-[var(--accent)] font-mono"
              />
              <select
                value={rule.policy}
                onChange={(e) => updateRule(i, 'policy', e.target.value)}
                className="px-2 py-1.5 text-sm rounded-lg border border-[var(--border-color)] bg-[var(--bg-primary)] text-[var(--text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-subtle)] focus:border-[var(--accent)]"
              >
                <option value="allow">{t('runtimeConfig.allow')}</option>
                <option value="ask">{t('runtimeConfig.ask')}</option>
                <option value="deny">{t('runtimeConfig.deny')}</option>
              </select>
              <input
                type="text"
                value={rule.description ?? ''}
                onChange={(e) => updateRule(i, 'description', e.target.value)}
                placeholder="description"
                className="px-2 py-1.5 text-sm rounded-lg border border-[var(--border-color)] bg-[var(--bg-primary)] text-[var(--text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-subtle)] focus:border-[var(--accent)]"
              />
              <button
                onClick={() => deleteRule(i)}
                className="p-1.5 text-[var(--text-secondary)] hover:text-red-500 transition-colors rounded-lg hover:bg-red-50 dark:hover:bg-red-900/20"
                title={t('runtimeConfig.deleteRule')}
              >
                <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                </svg>
              </button>
            </div>
          ))}

          <button
            onClick={addRule}
            className="flex items-center gap-1 text-sm text-[var(--accent-600)] hover:text-[var(--accent-700)] dark:text-[var(--accent-300)] transition-colors mt-2"
          >
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            {t('runtimeConfig.addRule')}
          </button>
        </div>
      </SettingsSection>

      <div className="flex gap-3">
        <button
          onClick={handleApply}
          disabled={applying}
          className="flex-1 px-4 py-2.5 text-sm font-medium text-white bg-[var(--accent-600)] hover:bg-[var(--accent-700)] disabled:opacity-50 rounded-xl transition-colors"
        >
          {applying ? t('common.loading') : t('runtimeConfig.apply')}
        </button>
      </div>
    </div>
  );
}

function SettingsSection({ title, children, action }: { title: string; children: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="bg-[var(--bg-card)] border border-[var(--border-color)] rounded-2xl overflow-hidden shadow-sm">
      <div className="px-5 py-4 border-b border-[var(--border-color)] flex items-center justify-between">
        <span className="text-sm font-medium text-[var(--text-primary)]">{title}</span>
        {action}
      </div>
      {children}
    </div>
  );
}
