import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { LLMProviders } from './LLMProviders';

const mockAddToast = vi.fn();
const mockClient = {
  adminListLLMProviders: vi.fn(),
  adminListLLMModels: vi.fn(),
  adminCreateLLMProvider: vi.fn(),
  adminUpdateLLMProvider: vi.fn(),
  adminDeleteLLMProvider: vi.fn(),
  adminCreateLLMModel: vi.fn(),
  adminUpdateLLMModel: vi.fn(),
  adminDeleteLLMModel: vi.fn(),
};

vi.mock('../../hooks/useNodeClient', () => ({
  useNodeClient: () => mockClient,
}));

vi.mock('../../store/toast', () => ({
  useToastStore: (selector: (state: { addToast: typeof mockAddToast }) => unknown) => selector({ addToast: mockAddToast }),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_key: string, fallback?: string) => fallback ?? _key,
  }),
}));

describe('LLMProviders write DTO payloads', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockClient.adminListLLMProviders.mockResolvedValue({
      providers: [
        {
          name: 'openai-main',
          provider_type: 'openai',
          base_url: 'https://api.example.test/v1',
          api_key: 'sk-****',
          is_default: false,
          enabled: true,
          api_format: 'chat',
          service_type: 'llm',
          config_json: '',
          created_at: '',
          updated_at: '',
        },
      ],
    });
    mockClient.adminListLLMModels.mockResolvedValue({
      models: [
        {
          name: 'gpt-main',
          provider_name: 'openai-main',
          model: 'gpt-5',
          base_url: 'https://model.example.test/v1',
          api_key: 'sk-model',
          is_default: false,
          enabled: true,
          config_json: '',
          created_at: '',
          updated_at: '',
        },
      ],
    });
    mockClient.adminCreateLLMProvider.mockResolvedValue(undefined);
    mockClient.adminUpdateLLMProvider.mockResolvedValue(undefined);
    mockClient.adminDeleteLLMProvider.mockResolvedValue(undefined);
    mockClient.adminCreateLLMModel.mockResolvedValue(undefined);
    mockClient.adminUpdateLLMModel.mockResolvedValue(undefined);
    mockClient.adminDeleteLLMModel.mockResolvedValue(undefined);
  });

  it('updates providers with only touched fields and preserves empty-string clearing', async () => {
    render(<LLMProviders />);

    await waitFor(() => {
      expect(screen.getByText('openai-main')).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText('编辑'));
    fireEvent.change(screen.getByDisplayValue('https://api.example.test/v1'), { target: { value: '' } });
    fireEvent.click(screen.getByText('保存'));

    await waitFor(() => {
      expect(mockClient.adminUpdateLLMProvider).toHaveBeenCalledWith('openai-main', { base_url: '' });
    });
  });

  it('updates models with only touched fields and preserves empty-string clearing', async () => {
    render(<LLMProviders />);

    await waitFor(() => {
      expect(screen.getByText('openai-main')).toBeInTheDocument();
    });

    fireEvent.click(screen.getAllByRole('button', { name: '' })[0]);

    await waitFor(() => {
      expect(screen.getByText('gpt-main')).toBeInTheDocument();
    });

    fireEvent.click(screen.getAllByText('编辑')[1]);
    fireEvent.change(screen.getByDisplayValue('https://model.example.test/v1'), { target: { value: '' } });
    fireEvent.click(screen.getByText('保存'));

    await waitFor(() => {
      expect(mockClient.adminUpdateLLMModel).toHaveBeenCalledWith('gpt-main', { base_url: '' });
    });
  });
});
