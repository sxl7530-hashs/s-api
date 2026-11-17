/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import React, { useRef, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, renderHook } from '@testing-library/react';
import axios from 'axios';
import { getFreshAuthHeaders } from '../../../helpers/api';
import { useApiRequest } from '../useApiRequest';

const transport = vi.hoisted(() => ({ streams: [] }));
vi.mock('sse.js', () => ({
  SSE: class {
    constructor(url, options) {
      this.url = url;
      this.options = options;
      this.listeners = {};
      this.readyState = 0;
      this.status = 200;
      transport.streams.push(this);
    }
    addEventListener(name, callback) {
      this.listeners[name] = callback;
    }
    stream() {}
    close() {
      this.readyState = 2;
    }
  },
}));
vi.mock('@douyinfe/semi-ui', () => ({
  Toast: { error: vi.fn() },
  Pagination: () => null,
}));
vi.mock('react-toastify', () => ({ toast: { error: vi.fn() } }));
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}));

function renderRequest() {
  return renderHook(() => {
    const [messages, setMessages] = useState([
      { role: 'assistant', content: '', status: 'loading' },
    ]);
    const source = useRef(null);
    const request = useApiRequest(
      setMessages,
      vi.fn(),
      vi.fn(),
      source,
      vi.fn(),
    );
    return { ...request, messages };
  });
}

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem(
    'user',
    JSON.stringify({
      id: 7,
      token: 'test-access',
      session: { sid: 'test-session' },
    }),
  );
  transport.streams.length = 0;
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('classic playground authenticated requests', () => {
  it.each([false, true])(
    'sends dashboard credentials for stream=%s',
    async (stream) => {
      const fetchRequest = vi.fn().mockResolvedValue({
        ok: true,
        json: async () => ({ choices: [{ message: { content: 'answer' } }] }),
      });
      vi.stubGlobal('fetch', fetchRequest);
      const hook = renderRequest();
      await act(async () => {
        hook.result.current.sendRequest({ stream }, stream);
      });
      const options = stream
        ? transport.streams[0].options
        : fetchRequest.mock.calls[0][1];
      expect(options.headers).toMatchObject({
        Authorization: 'Bearer test-access',
        'X-Auth-Session': 'test-session',
        'New-Api-User': 7,
      });
      if (stream) {
        expect(options.withCredentials).toBe(true);
        await act(async () => {
          transport.streams[0].listeners.message({
            data: JSON.stringify({
              choices: [{ delta: { content: 'answer' } }],
            }),
          });
          transport.streams[0].listeners.message({ data: '[DONE]' });
        });
      } else {
        expect(options.credentials).toBe('include');
      }
      expect(hook.result.current.messages[0]).toMatchObject({
        content: 'answer',
        status: 'complete',
      });
    },
  );

  it.each([false, true])(
    'does not start stream=%s after stopping during credential refresh',
    async (stream) => {
      localStorage.setItem(
        'user',
        JSON.stringify({
          id: 7,
          token: 'old',
          access_expires_at: 1,
          session: { sid: 'test-session' },
        }),
      );
      let resolveRefresh;
      vi.spyOn(axios, 'post').mockReturnValue(
        new Promise((resolve) => {
          resolveRefresh = resolve;
        }),
      );
      const fetchRequest = vi.fn();
      vi.stubGlobal('fetch', fetchRequest);
      const hook = renderRequest();
      await act(async () => {
        hook.result.current.sendRequest({ stream }, stream);
      });
      act(() => {
        hook.result.current.onStopGenerator();
      });
      await act(async () => {
        resolveRefresh({
          data: {
            success: true,
            data: {
              access_token: 'fresh',
              user: { id: 7 },
              session: { sid: 'test-session' },
            },
          },
        });
      });
      expect(fetchRequest).not.toHaveBeenCalled();
      expect(transport.streams).toHaveLength(0);
      expect(hook.result.current.messages[0].status).toBe('complete');
    },
  );

  it('aborts an ordinary request when generation is stopped', async () => {
    let signal;
    vi.stubGlobal(
      'fetch',
      vi.fn((url, options) => {
        signal = options.signal;
        return new Promise((resolve, reject) => {
          signal?.addEventListener('abort', () =>
            reject(new DOMException('Stopped', 'AbortError')),
          );
        });
      }),
    );
    const hook = renderRequest();
    await act(async () => {
      hook.result.current.sendRequest({ stream: false }, false);
    });
    await act(async () => {
      hook.result.current.onStopGenerator();
    });
    expect(signal?.aborted).toBe(true);
    expect(hook.result.current.messages[0].status).toBe('complete');
  });

  it.each([401, 403])(
    'does not reuse credentials after refresh returns %s',
    async (status) => {
      localStorage.setItem(
        'user',
        JSON.stringify({
          id: 7,
          token: 'old',
          access_expires_at: Math.floor(Date.now() / 1000) + 30,
          session: { sid: 'test-session' },
        }),
      );
      vi.spyOn(axios, 'post').mockRejectedValue({ response: { status } });
      await expect(getFreshAuthHeaders()).rejects.toMatchObject({
        response: { status },
      });
      expect(localStorage.getItem('user')).toBeNull();
    },
  );

  it('uses renewed credentials when the access token expires', async () => {
    localStorage.setItem(
      'user',
      JSON.stringify({
        id: 7,
        token: 'old',
        access_expires_at: 1,
        session: { sid: 'test-session' },
      }),
    );
    vi.spyOn(axios, 'post').mockResolvedValue({
      data: {
        success: true,
        data: {
          access_token: 'fresh',
          user: { id: 7 },
          session: { sid: 'test-session' },
        },
      },
    });
    expect(await getFreshAuthHeaders()).toMatchObject({
      Authorization: 'Bearer fresh',
    });
  });
});
