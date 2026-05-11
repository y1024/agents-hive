import { render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { WeChatConnectionPanel } from '../WeChatConnectionPanel';

const qrcodeMock = vi.hoisted(() => ({
  toDataURL: vi.fn<(value: string, options?: unknown) => Promise<string>>(),
}));
const refresh = vi.fn();
const login = vi.fn();
const relogin = vi.fn();
const logout = vi.fn();

vi.mock('qrcode', () => ({
  default: {
    toDataURL: qrcodeMock.toDataURL,
  },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_key: string, fallback?: string) => fallback ?? _key,
  }),
}));

vi.mock('../../../hooks/useWechatConnection', () => ({
  useWechatConnection: () => ({
    status: {
      enabled: true,
      status: 'waiting_qr_scan',
      conversation_count: 1,
    },
    conversations: [{
      peer_wxid: 'wx-peer',
      peer_nickname: '客户 A',
      peer_avatar_url: '',
      chat_type: 'direct',
      last_message_at: '2026-05-11T10:00:00Z',
    }],
    qrUrl: 'https://liteapp.weixin.qq.com/q/7GiQu1?qrcode=test&bot_type=3',
    lastEvent: null,
    loading: false,
    actionLoading: null,
    streamConnected: true,
    error: '',
    refresh,
    login,
    relogin,
    logout,
  }),
}));

describe('WeChatConnectionPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders an SDK login URL as a generated QR image instead of using the URL as img src', async () => {
    const toDataURL = qrcodeMock.toDataURL;
    toDataURL.mockResolvedValue('data:image/png;base64,qr-image');

    render(
      <MemoryRouter>
        <WeChatConnectionPanel />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(toDataURL).toHaveBeenCalledWith(
        'https://liteapp.weixin.qq.com/q/7GiQu1?qrcode=test&bot_type=3',
        expect.objectContaining({ width: 240 }),
      );
    });

    const image = await screen.findByRole('img', { name: '微信 Bot 连接二维码' });
    expect(image).toHaveAttribute('src', 'data:image/png;base64,qr-image');
    expect(image).not.toHaveAttribute(
      'src',
      'https://liteapp.weixin.qq.com/q/7GiQu1?qrcode=test&bot_type=3',
    );
  });

  it('states the official Bot boundary and does not render an IM session link', async () => {
    qrcodeMock.toDataURL.mockResolvedValue('data:image/png;base64,qr-image');

    render(
      <MemoryRouter>
        <WeChatConnectionPanel />
      </MemoryRouter>,
    );

    expect(screen.getByText(/客户给微信里的 clawbot 发消息后/)).toBeInTheDocument();
    expect(screen.getByText(/不会读取你微信号本人的普通私聊/)).toBeInTheDocument();
    expect(screen.getByText('客户 A')).toBeInTheDocument();
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.queryByText(/im-wechatbot/)).not.toBeInTheDocument();
    expect(await screen.findByRole('img', { name: '微信 Bot 连接二维码' })).toBeInTheDocument();
  });
});
