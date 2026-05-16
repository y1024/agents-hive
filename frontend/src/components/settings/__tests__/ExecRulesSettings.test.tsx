import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ExecRulesSettings } from '../ExecRulesSettings';

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

describe('ExecRulesSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    t.mockImplementation((key: string) => key);
    getRuntimeConfig.mockResolvedValue({
      security: {
        default_policy: 'allow',
        permission_mode: 'strict',
        exec_rules: [{ pattern: '^git\\s+', policy: 'ask', description: 'git writes' }],
      },
    });
  });

  it('loads and persists the unified permission mode switch', async () => {
    render(<ExecRulesSettings />);

    const modeSelect = await screen.findByLabelText('runtimeConfig.permissionMode');
    expect(modeSelect).toHaveValue('strict');
    fireEvent.change(modeSelect, { target: { value: 'minimal' } });
    await waitFor(() => expect(modeSelect).toHaveValue('minimal'));
    fireEvent.click(screen.getByText('runtimeConfig.apply'));

    await waitFor(() => {
      expect(updateRuntimeConfig).toHaveBeenCalledWith({
        security: {
          default_policy: 'allow',
          permission_mode: 'minimal',
          exec_rules: [{ pattern: '^git\\s+', policy: 'ask', description: 'git writes' }],
        },
      });
    });
  });
});
