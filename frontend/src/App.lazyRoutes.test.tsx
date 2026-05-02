import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const loadedRoutes = vi.hoisted(() => new Set<string>());

vi.mock('./pages/Login', () => ({
  Login: () => <div>login route</div>,
}));

vi.mock('./hooks/useTheme', () => ({
  useTheme: () => undefined,
}));

vi.mock('./hooks/useLanguage', () => ({
  useLanguage: () => undefined,
}));

vi.mock('./components/AuthGuard', () => ({
  AuthGuard: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

vi.mock('./components/AdminGuard', () => ({
  AdminGuard: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

vi.mock('./pages/Chat', () => {
  loadedRoutes.add('chat');
  return { Chat: () => <div>chat route</div> };
});

vi.mock('./pages/Guide', () => {
  loadedRoutes.add('guide');
  return { Guide: () => <div>guide route</div> };
});

vi.mock('./pages/admin/qualityworkbench/QualityWorkbench', () => {
  loadedRoutes.add('quality-workbench');
  return { QualityWorkbench: () => <div>quality workbench route</div> };
});

vi.mock('./pages/admin/MemoryGovernance', () => {
  loadedRoutes.add('memory-governance');
  return { MemoryGovernance: () => <div>memory governance route</div> };
});

vi.mock('./pages/admin/AutoOptimization', () => {
  loadedRoutes.add('auto-optimization');
  return { AutoOptimization: () => <div>auto optimization route</div> };
});

vi.mock('./pages/admin/MultiAgentEcosystem', () => {
  loadedRoutes.add('multi-agent');
  return { MultiAgentEcosystem: () => <div>multi agent route</div> };
});

describe('App route splitting', () => {
  beforeEach(() => {
    vi.resetModules();
    loadedRoutes.clear();
    window.history.pushState({}, '', '/login');
    window.localStorage.clear();
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      writable: true,
      value: vi.fn().mockImplementation((query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    });
  });

  it('does not load heavy route modules before their route is rendered', async () => {
    const { default: App } = await import('./App');

    render(<App />);

    expect(screen.getByText('login route')).toBeInTheDocument();
    expect(loadedRoutes.has('chat')).toBe(false);
    expect(loadedRoutes.has('guide')).toBe(false);
    expect(loadedRoutes.has('quality-workbench')).toBe(false);
    expect(loadedRoutes.has('memory-governance')).toBe(false);
    expect(loadedRoutes.has('auto-optimization')).toBe(false);
    expect(loadedRoutes.has('multi-agent')).toBe(false);
  });
});
