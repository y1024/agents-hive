import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { MCPServersSettings } from '../MCPServersSettings';

const getRuntimeConfig = vi.fn();
const updateRuntimeConfig = vi.fn();
const reloadMCP = vi.fn();
const listMCPTools = vi.fn();
const addToast = vi.fn();
const t = vi.fn((key: string) => key);
const nodeClient = {
  getRuntimeConfig,
  updateRuntimeConfig,
  reloadMCP,
  listMCPTools,
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

describe('MCPServersSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    t.mockImplementation((key: string) => key);
    getRuntimeConfig.mockResolvedValue({
      mcp: {
        timeout: 30_000_000_000,
        servers: {
          old: {
            transport: 'http',
            url: 'https://old.example.com/mcp',
            headers: { Authorization: '[REDACTED]' },
          },
          keep: {
            transport: 'http',
            url: 'https://keep.example.com/mcp',
          },
        },
      },
    });
    listMCPTools.mockResolvedValue({ servers: [], total: 0, mcp_count: 0 });
    reloadMCP.mockResolvedValue({ status: 'reloaded', servers: [] });
  });

  it('sends null tombstones for deleted MCP servers', async () => {
    render(<MCPServersSettings />);

    await screen.findByDisplayValue('old');
    const deleteButtons = screen.getAllByTitle('runtimeConfig.deleteMcpServer');
    fireEvent.click(deleteButtons[0]);
    fireEvent.click(screen.getByText('runtimeConfig.apply'));

    await waitFor(() => {
      expect(updateRuntimeConfig).toHaveBeenCalledWith({
        mcp: {
          timeout: '30s',
          servers: expect.objectContaining({
            old: null,
            keep: expect.objectContaining({
              transport: 'http',
              url: 'https://keep.example.com/mcp',
            }),
          }),
        },
      });
    });
  });
});
