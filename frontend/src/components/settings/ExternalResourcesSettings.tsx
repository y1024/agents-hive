import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNodeClient } from '../../hooks/useNodeClient';
import { useToastStore } from '../../store/toast';
import type { ExternalResource, ExternalResourceSaveRequest } from '../../types/api';

const TYPE_OPTIONS = ['database', 'monitoring', 'api', 'cache'] as const;
const ENV_OPTIONS = ['production', 'staging', 'testing', 'development'] as const;

export function ExternalResourcesSettings() {
  const { t } = useTranslation();
  const client = useNodeClient();
  const showToast = useToastStore((s) => s.addToast);

  const [resources, setResources] = useState<ExternalResource[]>([]);
  const [, setLoading] = useState(false);
  const [showForm, setShowForm] = useState(false);

  const loadData = useCallback(async () => {
    setLoading(true);
    try {
      const list = await client.listExternalResources();
      setResources(list || []);
    } catch (e) {
      console.error(t('externalResources.loadFail'), e);
    } finally {
      setLoading(false);
    }
  }, [client, t]);

  useEffect(() => {
    loadData();
  }, [loadData]);

  const handleDelete = async (name: string) => {
    if (!window.confirm(t('externalResources.deleteConfirm', { name }))) return;
    try {
      await client.deleteExternalResource(name);
      showToast('success', t('externalResources.deleteSuccess', { name }));
      await loadData();
    } catch (e) {
      const msg = e instanceof Error ? e.message : t('externalResources.deleteFail');
      showToast('error', msg);
    }
  };

  return (
    <div className="p-6 max-w-5xl mx-auto">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-lg font-semibold text-[var(--text-primary)]">{t('externalResources.title')}</h2>
          <p className="text-sm text-[var(--text-secondary)] mt-1">
            {t('externalResources.description')}
          </p>
        </div>
      </div>

      {/* 资源列表 */}
      {resources.length > 0 ? (
        <div className="space-y-4 mb-6">
          {resources.map((resource) => (
            <ResourceCard
              key={resource.name}
              resource={resource}
              onDelete={handleDelete}
            />
          ))}
        </div>
      ) : (
        <div className="mb-6 p-8 text-center bg-[var(--bg-card)] border border-[var(--border-color)] rounded-xl">
          <p className="text-sm text-[var(--text-secondary)]">{t('externalResources.empty')}</p>
        </div>
      )}

      {/* 添加资源表单 */}
      <div className="bg-[var(--bg-card)] border border-[var(--border-color)] rounded-2xl overflow-hidden shadow-sm">
        <button
          onClick={() => setShowForm(!showForm)}
          className="w-full px-5 py-4 text-left text-sm font-medium text-[var(--accent-600)] dark:text-[var(--accent-300)] hover:bg-[var(--bg-secondary)] transition-colors"
        >
          {showForm ? t('externalResources.collapseForm') : t('externalResources.addResource')}
        </button>
        {showForm && (
          <AddResourceForm
            onSuccess={() => {
              setShowForm(false);
              loadData();
            }}
          />
        )}
      </div>
    </div>
  );
}

/** 资源卡片 */
function ResourceCard({
  resource,
  onDelete,
}: {
  resource: ExternalResource;
  onDelete: (name: string) => void;
}) {
  const { t } = useTranslation();

  const typeBadgeClass = (() => {
    switch (resource.type) {
      case 'database':
        return 'bg-[var(--accent-50)] text-[var(--accent-700)] dark:bg-[var(--accent-light)] dark:text-[var(--accent-300)]';
      case 'monitoring':
        return 'bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300';
      case 'api':
        return 'bg-[var(--bg-secondary)] text-[var(--text-secondary)]';
      case 'cache':
        return 'bg-orange-100 text-orange-700 dark:bg-orange-900 dark:text-orange-300';
      default:
        return 'bg-[var(--bg-secondary)] text-[var(--text-secondary)]';
    }
  })();

  const envBadgeClass = (() => {
    switch (resource.environment) {
      case 'production':
        return 'bg-red-100 text-red-700 dark:bg-red-900 dark:text-red-300';
      case 'staging':
        return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900 dark:text-yellow-300';
      case 'testing':
        return 'bg-cyan-100 text-cyan-700 dark:bg-cyan-900 dark:text-cyan-300';
      case 'development':
        return 'bg-[var(--bg-secondary)] text-[var(--text-secondary)]';
      default:
        return 'bg-[var(--bg-secondary)] text-[var(--text-secondary)]';
    }
  })();

  return (
    <div className="bg-[var(--bg-card)] border border-[var(--border-color)] rounded-2xl overflow-hidden shadow-sm">
      <div className="px-5 py-4">
        <div className="flex items-center justify-between mb-2">
          <div className="flex items-center gap-3">
            <span className="text-sm font-medium text-[var(--text-primary)]">{resource.name}</span>
            <span className={`text-xs px-2 py-0.5 rounded-full font-medium ${typeBadgeClass}`}>
              {resource.type}
            </span>
            <span className={`text-xs px-2 py-0.5 rounded-full font-medium ${envBadgeClass}`}>
              {resource.environment}
            </span>
            {!resource.enabled && (
              <span className="text-xs px-2 py-0.5 rounded-full font-medium bg-[var(--bg-secondary)] text-[var(--text-secondary)]">
                {t('externalResources.disabled')}
              </span>
            )}
            {resource.read_only && (
              <span className="text-xs px-2 py-0.5 rounded-full font-medium bg-yellow-100 text-yellow-700 dark:bg-yellow-900 dark:text-yellow-300">
                {t('externalResources.readOnly')}
              </span>
            )}
          </div>
          <button
            onClick={() => onDelete(resource.name)}
            className="px-3 py-1 text-xs text-red-600 hover:bg-red-50 dark:hover:bg-red-900/20 rounded transition-colors"
          >
            {t('externalResources.delete')}
          </button>
        </div>
        {resource.description && (
          <p className="text-xs text-[var(--text-secondary)] mb-2">{resource.description}</p>
        )}
        <div className="flex flex-wrap gap-x-4 gap-y-1">
          {resource.connection && (
            <span className="text-xs text-[var(--text-secondary)]">
              <span className="font-medium">{t('externalResources.connection')}</span> {resource.connection}
            </span>
          )}
          {resource.endpoint && (
            <span className="text-xs text-[var(--text-secondary)]">
              <span className="font-medium">{t('externalResources.endpoint')}</span> {resource.endpoint}
            </span>
          )}
        </div>
      </div>
    </div>
  );
}

/** 添加资源表单 */
function AddResourceForm({ onSuccess }: { onSuccess: () => void }) {
  const { t } = useTranslation();
  const client = useNodeClient();
  const showToast = useToastStore((s) => s.addToast);

  const [name, setName] = useState('');
  const [type, setType] = useState<string>('database');
  const [environment, setEnvironment] = useState<string>('development');
  const [description, setDescription] = useState('');
  const [connection, setConnection] = useState('');
  const [endpoint, setEndpoint] = useState('');
  const [readOnly, setReadOnly] = useState(false);
  const [enabled, setEnabled] = useState(true);
  const [saving, setSaving] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});

  const validate = (): boolean => {
    const errs: Record<string, string> = {};
    if (!name.trim()) errs.name = t('externalResources.nameRequired');
    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const handleSubmit = async () => {
    if (!validate()) return;
    setSaving(true);
    try {
      const resource: ExternalResourceSaveRequest = {
        name: name.trim(),
        type,
        environment,
        description: description.trim(),
        connection: connection.trim(),
        endpoint: endpoint.trim(),
        read_only: readOnly,
        enabled,
      };
      await client.upsertExternalResource(resource);
      showToast('success', t('externalResources.saveSuccess', { name: resource.name }));
      onSuccess();
    } catch (e) {
      const msg = e instanceof Error ? e.message : t('externalResources.saveFail');
      showToast('error', msg);
    } finally {
      setSaving(false);
    }
  };

  const inputClass = 'w-full px-3 py-1.5 text-sm bg-[var(--bg-input)] border border-[var(--border-color)] rounded';

  return (
    <div className="p-5 space-y-4 bg-[var(--bg-primary)] border-t border-[var(--border-color)]">
      {/* 名称 */}
      <div>
        <label className="block text-sm text-[var(--text-secondary)] mb-1">
          {t('externalResources.nameLabel')} <span className="text-red-500">*</span>
        </label>
        <input
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={t('externalResources.namePlaceholder')}
          className={inputClass}
        />
        {errors.name && <p className="text-xs text-red-500 mt-1">{errors.name}</p>}
      </div>

      {/* 类型 */}
      <div>
        <label className="block text-sm text-[var(--text-secondary)] mb-1">{t('externalResources.typeLabel')}</label>
        <select
          value={type}
          onChange={(e) => setType(e.target.value)}
          className={inputClass}
        >
          {TYPE_OPTIONS.map((t) => (
            <option key={t} value={t}>{t}</option>
          ))}
        </select>
      </div>

      {/* 环境 */}
      <div>
        <label className="block text-sm text-[var(--text-secondary)] mb-1">{t('externalResources.envLabel')}</label>
        <select
          value={environment}
          onChange={(e) => setEnvironment(e.target.value)}
          className={inputClass}
        >
          {ENV_OPTIONS.map((env) => (
            <option key={env} value={env}>{env}</option>
          ))}
        </select>
      </div>

      {/* 描述 */}
      <div>
        <label className="block text-sm text-[var(--text-secondary)] mb-1">{t('externalResources.descLabel')}</label>
        <input
          type="text"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder={t('externalResources.descPlaceholder')}
          className={inputClass}
        />
      </div>

      {/* 连接信息 */}
      <div>
        <label className="block text-sm text-[var(--text-secondary)] mb-1">{t('externalResources.connLabel')}</label>
        <input
          type="text"
          value={connection}
          onChange={(e) => setConnection(e.target.value)}
          placeholder={t('externalResources.connPlaceholder')}
          className={inputClass}
        />
      </div>

      {/* 端点 */}
      <div>
        <label className="block text-sm text-[var(--text-secondary)] mb-1">{t('externalResources.endpointLabel')}</label>
        <input
          type="text"
          value={endpoint}
          onChange={(e) => setEndpoint(e.target.value)}
          placeholder={t('externalResources.endpointPlaceholder')}
          className={inputClass}
        />
      </div>

      {/* 复选框行 */}
      <div className="flex items-center gap-6">
        <label className="flex items-center gap-2 text-sm text-[var(--text-secondary)]">
          <input
            type="checkbox"
            checked={readOnly}
            onChange={(e) => setReadOnly(e.target.checked)}
            className="rounded border-[var(--border-color)]"
          />
          {t('externalResources.readOnly')}
        </label>
        <label className="flex items-center gap-2 text-sm text-[var(--text-secondary)]">
          <input
            type="checkbox"
            checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)}
            className="rounded border-[var(--border-color)]"
          />
          {t('externalResources.enabled')}
        </label>
      </div>

      {/* 提交 */}
      <div className="pt-2">
        <button
          onClick={handleSubmit}
          disabled={saving}
          className="px-4 py-2 text-sm bg-[var(--accent-600)] hover:bg-[var(--accent-700)] disabled:opacity-40 text-white rounded transition-colors"
        >
          {saving ? t('externalResources.saving') : t('externalResources.save')}
        </button>
      </div>
    </div>
  );
}
