import { useEffect, useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { useNodeClient } from '../../hooks/useNodeClient';
import { useToastStore } from '../../store/toast';
import type { PermissionRule, RuntimeConfig } from '../../types/api';

const DEFAULT_RULES: PermissionRule[] = [
  { tool_name: 'read_file', action: 'allow' },
  { tool_name: 'write_file', action: 'allow' },
  { tool_name: 'edit', action: 'allow' },
  { tool_name: 'multiedit', action: 'allow' },
  { tool_name: 'multi_edit', action: 'allow' },
  { tool_name: 'apply_patch', action: 'allow' },
  { tool_name: 'glob', action: 'allow' },
  { tool_name: 'grep', action: 'allow' },
  { tool_name: 'ls', action: 'allow' },
  { tool_name: 'websearch', action: 'allow' },
  { tool_name: 'webfetch', action: 'allow' },
  { tool_name: 'browser_interact', action: 'allow' },
  { tool_name: 'send_im_message', action: 'allow' },
  { tool_name: 'bash', action: 'ask' },
  { tool_name: 'shell', action: 'ask' },
  { tool_name: 'exec', action: 'ask' },
  { tool_name: 'run_command', action: 'ask' },
  { tool_name: 'feishu_api', pattern: 'create_approval', action: 'ask' },
  { tool_name: 'feishu_api', pattern: 'create_bitable_record', action: 'ask' },
  { tool_name: 'feishu_api', pattern: 'create_task', action: 'ask' },
  { tool_name: 'feishu_api', pattern: 'complete_task', action: 'ask' },
  { tool_name: 'feishu_api', pattern: 'update_bitable_record', action: 'ask' },
  { tool_name: 'feishu_api', pattern: 'write_sheet', action: 'ask' },
  { tool_name: 'feishu_api', action: 'allow' },
  { tool_name: 'memory', pattern: 'delete', action: 'ask' },
  { tool_name: 'memory', action: 'allow' },
  { tool_name: 'taskboard', pattern: 'delete', action: 'ask' },
  { tool_name: 'taskboard', action: 'allow' },
];

export function PermissionRulesSettings() {
  const { t } = useTranslation();
  const client = useNodeClient();
  const addToast = useToastStore((s) => s.addToast);

  const [rules, setRules] = useState<PermissionRule[]>([]);
  const [hitlEnabled, setHitlEnabled] = useState(false);
  const [loading, setLoading] = useState(true);
  const [applying, setApplying] = useState(false);

  const loadConfig = useCallback(async () => {
    setLoading(true);
    try {
      const cfg: RuntimeConfig = await client.getRuntimeConfig();
      if (cfg.hitl?.permission_rules) {
        setRules(cfg.hitl.permission_rules);
      }
      setHitlEnabled(cfg.hitl?.enabled ?? false);
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
        hitl: {
          enabled: hitlEnabled,
          permission_rules: rules,
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

  const updateRule = (index: number, field: keyof PermissionRule, value: string) => {
    setRules((prev) => prev.map((r, i) => (i === index ? { ...r, [field]: value } : r)));
  };

  const deleteRule = (index: number) => {
    setRules((prev) => prev.filter((_, i) => i !== index));
  };

  const addRule = () => {
    setRules((prev) => [...prev, { tool_name: '', action: 'ask' }]);
  };

  const resetDefaults = () => {
    setRules([...DEFAULT_RULES]);
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
        title={t('runtimeConfig.permissionRules')}
        action={
          <div className="flex items-center gap-3">
            <label className="flex items-center gap-2 cursor-pointer">
              <span className="text-xs text-[var(--text-secondary)]">{t('runtimeConfig.hitlEnabled')}</span>
              <button
                type="button"
                role="switch"
                aria-checked={hitlEnabled}
                onClick={() => setHitlEnabled((v) => !v)}
                className={`relative inline-flex h-5 w-9 shrink-0 rounded-full transition-colors focus:outline-none ${
                  hitlEnabled ? 'bg-[var(--accent-600)]' : 'bg-[var(--bg-secondary)]'
                }`}
              >
                <span
                  className={`inline-block h-4 w-4 rounded-full bg-white shadow transform transition-transform mt-0.5 ${
                    hitlEnabled ? 'translate-x-4' : 'translate-x-0.5'
                  }`}
                />
              </button>
            </label>
            <button
              onClick={resetDefaults}
              className="text-xs text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors"
            >
              {t('runtimeConfig.resetDefaults')}
            </button>
          </div>
        }
      >
        <div className="p-5 space-y-4">
          <p className="text-xs text-[var(--text-secondary)] mb-3">
            {t('runtimeConfig.permissionRulesHint')}
          </p>

          <div className="grid grid-cols-[1fr_120px_1fr_40px] gap-2 text-xs font-medium text-[var(--text-secondary)] px-1">
            <span>{t('runtimeConfig.toolName')}</span>
            <span>{t('runtimeConfig.action')}</span>
            <span>{t('runtimeConfig.pattern')}</span>
            <span></span>
          </div>

          {rules.map((rule, i) => (
            <div key={i} className="grid grid-cols-[1fr_120px_1fr_40px] gap-2 items-center">
              <input
                type="text"
                value={rule.tool_name}
                onChange={(e) => updateRule(i, 'tool_name', e.target.value)}
                placeholder="bash"
                className="px-2 py-1.5 text-sm rounded-lg border border-[var(--border-color)] bg-[var(--bg-primary)] text-[var(--text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-subtle)] focus:border-[var(--accent)]"
              />
              <select
                value={rule.action}
                onChange={(e) => updateRule(i, 'action', e.target.value)}
                className="px-2 py-1.5 text-sm rounded-lg border border-[var(--border-color)] bg-[var(--bg-primary)] text-[var(--text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-subtle)] focus:border-[var(--accent)]"
              >
                <option value="allow">{t('runtimeConfig.allow')}</option>
                <option value="ask">{t('runtimeConfig.ask')}</option>
                <option value="deny">{t('runtimeConfig.deny')}</option>
              </select>
              <input
                type="text"
                value={rule.pattern ?? ''}
                onChange={(e) => updateRule(i, 'pattern', e.target.value)}
                placeholder="src/**/*.go"
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
