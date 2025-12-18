import { writable } from 'svelte/store';
import { browser } from '$app/environment';
import { goto } from '$app/navigation';
import { base } from '$app/paths';

function createAuthStore() {
  const { subscribe, set, update } = writable({
    user: null,
    accessToken: null,
    refreshToken: null,
    isAuthenticated: false,
    isLoading: true
  });

  return {
    subscribe,

    init() {
      if (!browser) return;

      const accessToken = localStorage.getItem('accessToken');
      const refreshToken = localStorage.getItem('refreshToken');
      const userStr = localStorage.getItem('user');

      if (accessToken && userStr) {
        try {
          const user = JSON.parse(userStr);
          set({
            user,
            accessToken,
            refreshToken,
            isAuthenticated: true,
            isLoading: false
          });
        } catch {
          this.logout();
        }
      } else {
        set({
          user: null,
          accessToken: null,
          refreshToken: null,
          isAuthenticated: false,
          isLoading: false
        });
      }
    },

    login(accessToken, refreshToken, user) {
      if (browser) {
        localStorage.setItem('accessToken', accessToken);
        localStorage.setItem('refreshToken', refreshToken);
        localStorage.setItem('user', JSON.stringify(user));
      }
      set({
        user,
        accessToken,
        refreshToken,
        isAuthenticated: true,
        isLoading: false
      });
    },

    logout() {
      if (browser) {
        localStorage.removeItem('accessToken');
        localStorage.removeItem('refreshToken');
        localStorage.removeItem('user');
      }
      set({
        user: null,
        accessToken: null,
        refreshToken: null,
        isAuthenticated: false,
        isLoading: false
      });
      goto(base || '/admin');
    },

    updateTokens(accessToken, refreshToken) {
      if (browser) {
        localStorage.setItem('accessToken', accessToken);
        if (refreshToken) {
          localStorage.setItem('refreshToken', refreshToken);
        }
      }
      update(state => ({
        ...state,
        accessToken,
        refreshToken: refreshToken || state.refreshToken
      }));
    },

    getAccessToken() {
      if (browser) {
        return localStorage.getItem('accessToken');
      }
      return null;
    },

    getRefreshToken() {
      if (browser) {
        return localStorage.getItem('refreshToken');
      }
      return null;
    }
  };
}

export const auth = createAuthStore();
