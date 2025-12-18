<script>
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { base } from '$app/paths';
  import { auth } from '$lib/auth.js';
  import { api } from '$lib/api.js';
  import DashboardLayout from '$lib/DashboardLayout.svelte';

  let family = null;
  let members = [];
  let servers = [];
  let loading = true;
  let error = '';
  let isAdmin = false;
  let currentUserId = '';

  let showAddServerModal = false;
  let serverName = '';
  let serverType = 'plex';
  let serverUrl = '';
  let modalError = '';
  let modalLoading = false;

  const serverTypes = [
    { value: 'plex', label: 'Plex' },
    { value: 'jellyfin', label: 'Jellyfin' },
    { value: 'audiobookshelf', label: 'Audiobookshelf' },
    { value: 'kavita', label: 'Kavita' },
    { value: 'calibre', label: 'Calibre' },
    { value: 'calibre-web', label: 'Calibre-Web' },
    { value: 'storyteller', label: 'Storyteller' },
    { value: 'booklore', label: 'BookLore' }
  ];

  $: familyId = $page.params.id;

  onMount(() => {
    const unsubscribe = auth.subscribe(state => {
      if (!state.isLoading && !state.isAuthenticated) {
        goto(base || '/admin');
      }
      if (state.user) {
        currentUserId = state.user.id;
      }
    });

    loadFamily();
    return unsubscribe;
  });

  async function loadFamily() {
    loading = true;
    try {
      const data = await api.getFamily(familyId);
      family = data.family;
      members = data.members || [];

      const currentMember = members.find(m => m.userId === currentUserId);
      isAdmin = currentMember?.role === 'admin';

      servers = await api.getFamilyServers(familyId) || [];
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  async function regenerateInviteCode() {
    try {
      const data = await api.regenerateInviteCode(familyId);
      family.inviteCode = data.inviteCode;
      family = family;
    } catch (e) {
      alert('Failed to regenerate invite code: ' + e.message);
    }
  }

  async function removeMember(userId, username) {
    if (!confirm(`Are you sure you want to remove ${username} from this family?`)) return;
    try {
      await api.removeMember(familyId, userId);
      await loadFamily();
    } catch (e) {
      alert('Failed to remove member: ' + e.message);
    }
  }

  async function leaveFamily() {
    if (!confirm('Are you sure you want to leave this family?')) return;
    try {
      await api.leaveFamily(familyId);
      goto(`${base}/families`);
    } catch (e) {
      alert('Failed to leave family: ' + e.message);
    }
  }

  async function addServer() {
    modalError = '';
    modalLoading = true;
    try {
      await api.createServer(familyId, {
        name: serverName,
        type: serverType,
        url: serverUrl
      });
      showAddServerModal = false;
      serverName = '';
      serverType = 'plex';
      serverUrl = '';
      await loadFamily();
    } catch (e) {
      modalError = e.message;
    } finally {
      modalLoading = false;
    }
  }

  async function deleteServer(serverId, serverNameToDelete) {
    if (!confirm(`Are you sure you want to delete ${serverNameToDelete}?`)) return;
    try {
      await api.deleteServer(familyId, serverId);
      await loadFamily();
    } catch (e) {
      alert('Failed to delete server: ' + e.message);
    }
  }

  async function toggleAccess(serverId, userId, hasAccess, username) {
    try {
      if (hasAccess) {
        await api.revokeServerAccess(familyId, serverId, userId);
      } else {
        await api.grantServerAccess(familyId, serverId, userId);
      }
      await loadFamily();
    } catch (e) {
      alert(`Failed to ${hasAccess ? 'revoke' : 'grant'} access: ` + e.message);
    }
  }

  function copyInviteCode() {
    navigator.clipboard.writeText(family.inviteCode);
    alert('Invite code copied to clipboard!');
  }
</script>

<DashboardLayout title={family?.name || 'Family'}>
  {#if loading}
    <div class="flex items-center justify-center h-64">
      <div class="animate-spin rounded-full h-8 w-8 border-b-2 border-primary-600"></div>
    </div>
  {:else if error}
    <div class="bg-red-50 text-red-600 p-4 rounded-lg">{error}</div>
  {:else if family}
    <div class="mb-6">
      <a href="{base}/families" class="text-primary-600 hover:text-primary-700 text-sm flex items-center">
        <svg class="w-4 h-4 mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7" />
        </svg>
        Back to Families
      </a>
    </div>

    <div class="grid grid-cols-1 lg:grid-cols-3 gap-6">
      <!-- Family Info & Members -->
      <div class="lg:col-span-2 space-y-6">
        <!-- Members Section -->
        <div class="bg-white rounded-lg shadow">
          <div class="px-6 py-4 border-b flex items-center justify-between">
            <h3 class="text-lg font-semibold text-gray-900">Members ({members.length})</h3>
          </div>
          <div class="p-6">
            <div class="space-y-3">
              {#each members as member}
                <div class="flex items-center justify-between p-3 bg-gray-50 rounded-lg">
                  <div class="flex items-center">
                    <div class="w-10 h-10 bg-primary-100 rounded-full flex items-center justify-center">
                      <span class="text-primary-600 font-semibold">
                        {member.username.charAt(0).toUpperCase()}
                      </span>
                    </div>
                    <div class="ml-3">
                      <p class="font-medium text-gray-900">
                        {member.displayName || member.username}
                        {#if member.userId === currentUserId}
                          <span class="text-gray-400">(you)</span>
                        {/if}
                      </p>
                      <p class="text-sm text-gray-500">@{member.username}</p>
                    </div>
                  </div>
                  <div class="flex items-center gap-2">
                    <span class="px-2 py-1 text-xs rounded-full {member.role === 'admin' ? 'bg-purple-100 text-purple-700' : 'bg-gray-100 text-gray-600'}">
                      {member.role}
                    </span>
                    {#if isAdmin && member.userId !== currentUserId}
                      <button
                        on:click={() => removeMember(member.userId, member.username)}
                        class="text-red-600 hover:text-red-700 p-1"
                        title="Remove member"
                      >
                        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                        </svg>
                      </button>
                    {/if}
                  </div>
                </div>
              {/each}
            </div>
          </div>
        </div>

        <!-- Servers Section -->
        <div class="bg-white rounded-lg shadow">
          <div class="px-6 py-4 border-b flex items-center justify-between">
            <h3 class="text-lg font-semibold text-gray-900">Media Servers ({servers.length})</h3>
            {#if isAdmin}
              <button
                on:click={() => showAddServerModal = true}
                class="text-primary-600 hover:text-primary-700 text-sm flex items-center"
              >
                <svg class="w-4 h-4 mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
                </svg>
                Add Server
              </button>
            {/if}
          </div>
          <div class="p-6">
            {#if servers.length === 0}
              <p class="text-gray-500 text-center py-4">No servers configured yet.</p>
            {:else}
              <div class="space-y-4">
                {#each servers as server}
                  <div class="border rounded-lg p-4">
                    <div class="flex items-center justify-between mb-3">
                      <div>
                        <h4 class="font-medium text-gray-900">{server.name}</h4>
                        <p class="text-sm text-gray-500">{server.type} • {server.url}</p>
                      </div>
                      {#if isAdmin}
                        <button
                          on:click={() => deleteServer(server.id, server.name)}
                          class="text-red-600 hover:text-red-700 p-1"
                          title="Delete server"
                        >
                          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                          </svg>
                        </button>
                      {/if}
                    </div>

                    {#if isAdmin}
                      <div class="border-t pt-3 mt-3">
                        <p class="text-sm text-gray-600 mb-2">Member Access:</p>
                        <div class="flex flex-wrap gap-2">
                          {#each members as member}
                            <button
                              on:click={() => toggleAccess(server.id, member.userId, server.hasAccess && member.userId === currentUserId, member.username)}
                              class="px-3 py-1 text-sm rounded-full transition-colors
                                {server.accessList?.some(a => a.userId === member.userId) || member.role === 'admin'
                                  ? 'bg-green-100 text-green-700 hover:bg-green-200'
                                  : 'bg-gray-100 text-gray-600 hover:bg-gray-200'}"
                              disabled={member.role === 'admin'}
                              title={member.role === 'admin' ? 'Admins always have access' : 'Click to toggle access'}
                            >
                              {member.username}
                              {#if server.accessList?.some(a => a.userId === member.userId) || member.role === 'admin'}
                                <svg class="w-4 h-4 inline ml-1" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" />
                                </svg>
                              {/if}
                            </button>
                          {/each}
                        </div>
                      </div>
                    {/if}
                  </div>
                {/each}
              </div>
            {/if}
          </div>
        </div>
      </div>

      <!-- Sidebar -->
      <div class="space-y-6">
        <!-- Invite Code -->
        {#if isAdmin && family.inviteCode}
          <div class="bg-white rounded-lg shadow p-6">
            <h3 class="text-lg font-semibold text-gray-900 mb-4">Invite Code</h3>
            <div class="bg-gray-100 rounded-lg p-4 text-center mb-4">
              <code class="text-2xl font-mono text-primary-600">{family.inviteCode}</code>
            </div>
            <div class="flex gap-2">
              <button
                on:click={copyInviteCode}
                class="flex-1 px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 text-sm"
              >
                Copy Code
              </button>
              <button
                on:click={regenerateInviteCode}
                class="px-4 py-2 border border-gray-300 rounded-lg hover:bg-gray-50 text-sm"
                title="Generate new code"
              >
                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                </svg>
              </button>
            </div>
          </div>
        {/if}

        <!-- Actions -->
        <div class="bg-white rounded-lg shadow p-6">
          <h3 class="text-lg font-semibold text-gray-900 mb-4">Actions</h3>
          <button
            on:click={leaveFamily}
            class="w-full px-4 py-2 border border-red-300 text-red-600 rounded-lg hover:bg-red-50"
          >
            Leave Family
          </button>
        </div>
      </div>
    </div>
  {/if}
</DashboardLayout>

<!-- Add Server Modal -->
{#if showAddServerModal}
  <div class="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
    <div class="bg-white rounded-lg shadow-xl p-6 w-full max-w-md">
      <h3 class="text-lg font-semibold text-gray-900 mb-4">Add Media Server</h3>

      {#if modalError}
        <div class="bg-red-50 text-red-600 p-3 rounded-lg mb-4 text-sm">{modalError}</div>
      {/if}

      <form on:submit|preventDefault={addServer}>
        <div class="space-y-4">
          <div>
            <label for="serverName" class="block text-sm font-medium text-gray-700 mb-1">Server Name</label>
            <input
              id="serverName"
              type="text"
              bind:value={serverName}
              required
              class="w-full px-4 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-primary-500 focus:border-transparent"
              placeholder="My Plex Server"
            />
          </div>

          <div>
            <label for="serverType" class="block text-sm font-medium text-gray-700 mb-1">Server Type</label>
            <select
              id="serverType"
              bind:value={serverType}
              class="w-full px-4 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-primary-500 focus:border-transparent"
            >
              {#each serverTypes as type}
                <option value={type.value}>{type.label}</option>
              {/each}
            </select>
          </div>

          <div>
            <label for="serverUrl" class="block text-sm font-medium text-gray-700 mb-1">Server URL</label>
            <input
              id="serverUrl"
              type="url"
              bind:value={serverUrl}
              required
              class="w-full px-4 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-primary-500 focus:border-transparent"
              placeholder="https://plex.example.com"
            />
          </div>
        </div>

        <div class="flex gap-3 mt-6">
          <button
            type="button"
            on:click={() => { showAddServerModal = false; modalError = ''; }}
            class="flex-1 px-4 py-2 border border-gray-300 rounded-lg text-gray-700 hover:bg-gray-50"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={modalLoading}
            class="flex-1 px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 disabled:opacity-50"
          >
            {modalLoading ? 'Adding...' : 'Add Server'}
          </button>
        </div>
      </form>
    </div>
  </div>
{/if}
