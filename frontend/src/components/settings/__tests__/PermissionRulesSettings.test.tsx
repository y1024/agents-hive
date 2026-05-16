import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { PermissionRulesSettings } from '../PermissionRulesSettings';

const getRuntimeConfig = vi.fn();
const updateRuntimeConfig = vi.fn();
const addToast = vi.fn();
const t = vi.fn((key: string) => key);
const nodeClient = {
  getRuntimeConfig,
  updateRuntimeConfig,
};

vi.mock('../../../hooks/useNodeClient', () => ({
  useNodeClient: () => nodeClient,
}));

vi.mock('../../../store/toast', () => ({
  useToastStore: (selector: (state: { addToast: typeof addToast }) => unknown) => selector({ addToast }),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t,
  }),
}));

describe('PermissionRulesSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    t.mockImplementation((key: string) => key);
    getRuntimeConfig.mockResolvedValue({
      hitl: {
        enabled: false,
        permission_rules: [{ tool_name: 'bash', action: 'ask' }],
      },
    });
  });

  it('labels permission rules as legacy HITL rollback scope', async () => {
    render(<PermissionRulesSettings />);

    expect(await screen.findByText('runtimeConfig.permissionRules')).toBeInTheDocument();
    expect(screen.getByText('runtimeConfig.permissionRulesHint')).toBeInTheDocument();
  });

  it('resets to low-friction defaults aligned with unified ToolPolicy', async () => {
    render(<PermissionRulesSettings />);

    await screen.findByDisplayValue('bash');
    fireEvent.click(screen.getByText('runtimeConfig.resetDefaults'));
    await screen.findByDisplayValue('send_im_message');
    fireEvent.click(screen.getByText('runtimeConfig.apply'));

    await waitFor(() => {
      expect(updateRuntimeConfig).toHaveBeenCalledWith({
        hitl: {
          enabled: false,
          permission_rules: expect.arrayContaining([
            { tool_name: 'write_file', action: 'allow' },
            { tool_name: 'send_im_message', action: 'allow' },
            { tool_name: 'feishu_api', pattern: 'create_approval', action: 'ask' },
            { tool_name: 'feishu_api', action: 'allow' },
            { tool_name: 'memory', pattern: 'delete', action: 'ask' },
          ]),
        },
      });
    });
  });
});
