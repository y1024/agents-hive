import { useAuthStore, refreshToken } from '../store/auth';

const BASE_URL = import.meta.env.VITE_API_BASE || '';

/** 默认请求超时时间（毫秒） */
const DEFAULT_TIMEOUT = 30_000;

/** 不限时，用于 LLM 回复等长时间操作（传 0 表示无超时） */
const NO_TIMEOUT = 0;

// 防并发 401 多次跳转
let isRedirecting = false;

export class ApiClient {
  private baseUrl: string;
  private timeout: number;

  constructor(baseUrl: string = BASE_URL, timeout: number = DEFAULT_TIMEOUT) {
    this.baseUrl = baseUrl;
    this.timeout = timeout;
  }

  async request<T>(path: string, options: RequestInit & { timeout?: number } = {}): Promise<T> {
    const url = `${this.baseUrl}${path}`;
    const requestTimeout = options.timeout ?? this.timeout;

    // 使用 AbortController 实现超时（requestTimeout <= 0 表示不限时）
    const controller = new AbortController();
    const timeoutId = requestTimeout > 0
      ? setTimeout(() => controller.abort(), requestTimeout)
      : null;

    try {
      const token = localStorage.getItem('auth_token');
      const isFormData = typeof FormData !== 'undefined' && options.body instanceof FormData;
      const headers: Record<string, string> = {
        ...(isFormData ? {} : { 'Content-Type': 'application/json' }),
        ...(options.headers as Record<string, string>),
      };
      if (token) {
        headers['Authorization'] = `Bearer ${token}`;
      }

      const res = await fetch(url, {
        ...options,
        signal: options.signal ?? controller.signal,
        headers,
      });

      // 401 拦截：排除 /auth/ 路径防循环
      if (res.status === 401 && !path.startsWith('/api/v1/auth/')) {
        const newToken = await refreshToken();
        if (newToken) {
          headers['Authorization'] = `Bearer ${newToken}`;
          const retryRes = await fetch(url, {
            ...options,
            signal: options.signal ?? controller.signal,
            headers,
          });
          if (retryRes.ok) {
            if (retryRes.status === 204) return undefined as T;
            return retryRes.json();
          }
          // retry 仍失败：抛出实际错误而非静默跳转
          const retryBody = await retryRes.json().catch(() => ({ error: retryRes.statusText, code: retryRes.status }));
          throw new ApiRequestError(retryBody.error || retryRes.statusText, retryBody.code || retryRes.status);
        }
        if (!isRedirecting) {
          isRedirecting = true;
          useAuthStore.getState().clearAuth();
          // 跳转后重置 flag，防止 SPA 热重载或测试环境中 flag 永久锁死
          setTimeout(() => { isRedirecting = false; }, 5000);
          window.location.href = '/login';
        }
        throw new ApiRequestError('Unauthorized', 401);
      }

      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText, code: res.status }));
        throw new ApiRequestError(body.error || res.statusText, body.code || res.status);
      }

      // 204 No Content
      if (res.status === 204) return undefined as T;

      return res.json();
    } catch (err) {
      if (err instanceof ApiRequestError) throw err;
      if (err instanceof DOMException && err.name === 'AbortError') {
        throw new ApiRequestError(`请求超时 (${requestTimeout / 1000}s)`, 408);
      }
      throw err;
    } finally {
      if (timeoutId) clearTimeout(timeoutId);
    }
  }

  get<T>(path: string): Promise<T> {
    return this.request<T>(path);
  }

  post<T>(path: string, body?: unknown, opts?: { timeout?: number }): Promise<T> {
    return this.request<T>(path, {
      method: 'POST',
      body: body ? JSON.stringify(body) : undefined,
      timeout: opts?.timeout,
    });
  }

  /** POST without timeout for LLM responses and other long-running operations */
  postLong<T>(path: string, body?: unknown): Promise<T> {
    return this.post<T>(path, body, { timeout: NO_TIMEOUT });
  }

  postForm<T>(path: string, body: FormData, opts?: { timeout?: number }): Promise<T> {
    return this.request<T>(path, {
      method: 'POST',
      body,
      timeout: opts?.timeout,
    });
  }

  postFormLong<T>(path: string, body: FormData): Promise<T> {
    return this.postForm<T>(path, body, { timeout: NO_TIMEOUT });
  }

  put<T>(path: string, body: unknown): Promise<T> {
    return this.request<T>(path, {
      method: 'PUT',
      body: JSON.stringify(body),
    });
  }

  patch<T>(path: string, body: unknown): Promise<T> {
    return this.request<T>(path, {
      method: 'PATCH',
      body: JSON.stringify(body),
    });
  }

  delete<T>(path: string): Promise<T> {
    return this.request<T>(path, { method: 'DELETE' });
  }
}

export class ApiRequestError extends Error {
  code: number;
  constructor(message: string, code: number) {
    super(message);
    this.name = 'ApiRequestError';
    this.code = code;
  }
}

// 默认客户端实例
export const apiClient = new ApiClient();
