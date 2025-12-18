import { auth } from './auth.js';
import { browser } from '$app/environment';

const API_BASE = '/api/v1';

async function refreshAccessToken() {
  const refreshToken = auth.getRefreshToken();
  if (!refreshToken) {
    throw new Error('No refresh token');
  }

  const response = await fetch(`${API_BASE}/auth/refresh`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json'
    },
    body: JSON.stringify({ refreshToken })
  });

  if (!response.ok) {
    throw new Error('Failed to refresh token');
  }

  const data = await response.json();
  auth.updateTokens(data.accessToken, data.refreshToken);
  return data.accessToken;
}

async function fetchWithAuth(url, options = {}) {
  if (!browser) return null;

  let accessToken = auth.getAccessToken();

  const makeRequest = async (token) => {
    const headers = {
      'Content-Type': 'application/json',
      ...options.headers
    };

    if (token) {
      headers['Authorization'] = `Bearer ${token}`;
    }

    return fetch(url, {
      ...options,
      headers
    });
  };

  let response = await makeRequest(accessToken);

  // If unauthorized, try to refresh token
  if (response.status === 401 && accessToken) {
    try {
      accessToken = await refreshAccessToken();
      response = await makeRequest(accessToken);
    } catch {
      auth.logout();
      throw new Error('Session expired');
    }
  }

  return response;
}

export const api = {
  // Auth
  async login(email, password) {
    const response = await fetch(`${API_BASE}/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password })
    });

    if (!response.ok) {
      const error = await response.json();
      throw new Error(error.error || 'Login failed');
    }

    return response.json();
  },

  async register(username, email, password) {
    const response = await fetch(`${API_BASE}/auth/register`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, email, password })
    });

    if (!response.ok) {
      const error = await response.json();
      throw new Error(error.error || 'Registration failed');
    }

    return response.json();
  },

  // User
  async getMe() {
    const response = await fetchWithAuth(`${API_BASE}/me`);
    if (!response.ok) throw new Error('Failed to get user');
    return response.json();
  },

  // Families
  async getFamilies() {
    const response = await fetchWithAuth(`${API_BASE}/families`);
    if (!response.ok) throw new Error('Failed to get families');
    return response.json();
  },

  async getFamily(id) {
    const response = await fetchWithAuth(`${API_BASE}/families/${id}`);
    if (!response.ok) throw new Error('Failed to get family');
    return response.json();
  },

  async createFamily(name) {
    const response = await fetchWithAuth(`${API_BASE}/families`, {
      method: 'POST',
      body: JSON.stringify({ name })
    });
    if (!response.ok) {
      const error = await response.json();
      throw new Error(error.error || 'Failed to create family');
    }
    return response.json();
  },

  async joinFamily(inviteCode) {
    const response = await fetchWithAuth(`${API_BASE}/families/join`, {
      method: 'POST',
      body: JSON.stringify({ inviteCode })
    });
    if (!response.ok) {
      const error = await response.json();
      throw new Error(error.error || 'Failed to join family');
    }
    return response.json();
  },

  async leaveFamily(familyId) {
    const response = await fetchWithAuth(`${API_BASE}/families/${familyId}/leave`, {
      method: 'DELETE'
    });
    if (!response.ok) throw new Error('Failed to leave family');
    return response.json();
  },

  async removeMember(familyId, userId) {
    const response = await fetchWithAuth(`${API_BASE}/families/${familyId}/members/${userId}`, {
      method: 'DELETE'
    });
    if (!response.ok) throw new Error('Failed to remove member');
    return response.json();
  },

  async regenerateInviteCode(familyId) {
    const response = await fetchWithAuth(`${API_BASE}/families/${familyId}/invite-code`, {
      method: 'POST'
    });
    if (!response.ok) throw new Error('Failed to regenerate invite code');
    return response.json();
  },

  // Servers
  async getMyServers() {
    const response = await fetchWithAuth(`${API_BASE}/me/servers`);
    if (!response.ok) throw new Error('Failed to get servers');
    return response.json();
  },

  async getFamilyServers(familyId) {
    const response = await fetchWithAuth(`${API_BASE}/families/${familyId}/servers`);
    if (!response.ok) throw new Error('Failed to get servers');
    return response.json();
  },

  async createServer(familyId, server) {
    const response = await fetchWithAuth(`${API_BASE}/families/${familyId}/servers`, {
      method: 'POST',
      body: JSON.stringify(server)
    });
    if (!response.ok) {
      const error = await response.json();
      throw new Error(error.error || 'Failed to create server');
    }
    return response.json();
  },

  async updateServer(familyId, serverId, server) {
    const response = await fetchWithAuth(`${API_BASE}/families/${familyId}/servers/${serverId}`, {
      method: 'PUT',
      body: JSON.stringify(server)
    });
    if (!response.ok) throw new Error('Failed to update server');
    return response.json();
  },

  async deleteServer(familyId, serverId) {
    const response = await fetchWithAuth(`${API_BASE}/families/${familyId}/servers/${serverId}`, {
      method: 'DELETE'
    });
    if (!response.ok) throw new Error('Failed to delete server');
    return response.json();
  },

  async grantServerAccess(familyId, serverId, userId) {
    const response = await fetchWithAuth(`${API_BASE}/families/${familyId}/servers/${serverId}/access`, {
      method: 'POST',
      body: JSON.stringify({ userId })
    });
    if (!response.ok) {
      const error = await response.json();
      throw new Error(error.error || 'Failed to grant access');
    }
    return response.json();
  },

  async revokeServerAccess(familyId, serverId, userId) {
    const response = await fetchWithAuth(`${API_BASE}/families/${familyId}/servers/${serverId}/access/${userId}`, {
      method: 'DELETE'
    });
    if (!response.ok) throw new Error('Failed to revoke access');
    return response.json();
  }
};
