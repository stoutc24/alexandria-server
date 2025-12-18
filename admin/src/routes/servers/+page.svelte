<script>
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { base } from '$app/paths';
  import { auth } from '$lib/auth.js';
  import { api } from '$lib/api.js';
  import DashboardLayout from '$lib/DashboardLayout.svelte';

  let servers = [];
  let loading = true;
  let error = '';

  const serverIcons = {
    plex: { color: 'bg-yellow-100 text-yellow-700', label: 'Plex' },
    jellyfin: { color: 'bg-purple-100 text-purple-700', label: 'Jellyfin' },
    audiobookshelf: { color: 'bg-green-100 text-green-700', label: 'Audiobookshelf' },
    kavita: { color: 'bg-blue-100 text-blue-700', label: 'Kavita' },
    calibre: { color: 'bg-orange-100 text-orange-700', label: 'Calibre' },
    'calibre-web': { color: 'bg-orange-100 text-orange-700', label: 'Calibre-Web' },
    storyteller: { color: 'bg-pink-100 text-pink-700', label: 'Storyteller' },
    booklore: { color: 'bg-indigo-100 text-indigo-700', label: 'BookLore' }
  };

  onMount(() => {
    const unsubscribe = auth.subscribe(state => {
      if (!state.isLoading && !state.isAuthenticated) {
        goto(base || '/admin');
      }
    });

    loadServers();
    return unsubscribe;
  });

  async function loadServers() {
    try {
      servers = await api.getMyServers() || [];
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  function getServerIcon(type) {
    return serverIcons[type] || { color: 'bg-gray-100 text-gray-700', label: type };
  }

  function openServer(url) {
    window.open(url, '_blank');
  }
</script>

<DashboardLayout title="My Servers">
  {#if loading}
    <div class="flex items-center justify-center h-64">
      <div class="animate-spin rounded-full h-8 w-8 border-b-2 border-primary-600"></div>
    </div>
  {:else if error}
    <div class="bg-red-50 text-red-600 p-4 rounded-lg">{error}</div>
  {:else if servers.length === 0}
    <div class="bg-white rounded-lg shadow p-12 text-center">
      <svg class="w-16 h-16 mx-auto text-gray-300 mb-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-2-4h.01M17 16h.01" />
      </svg>
      <h3 class="text-lg font-medium text-gray-900 mb-2">No servers yet</h3>
      <p class="text-gray-500 mb-4">You don't have access to any media servers.</p>
      <a
        href="{base}/families"
        class="text-primary-600 hover:text-primary-700"
      >
        Join or create a family to add servers →
      </a>
    </div>
  {:else}
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
      {#each servers as server}
        {@const icon = getServerIcon(server.type)}
        <div class="bg-white rounded-lg shadow hover:shadow-lg transition-shadow">
          <div class="p-6">
            <div class="flex items-center mb-4">
              <div class="w-12 h-12 {icon.color} rounded-lg flex items-center justify-center">
                <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-2-4h.01M17 16h.01" />
                </svg>
              </div>
              <div class="ml-4">
                <span class="px-2 py-1 text-xs rounded-full {icon.color}">
                  {icon.label}
                </span>
              </div>
            </div>
            <h3 class="text-lg font-semibold text-gray-900 mb-1">{server.name}</h3>
            <p class="text-sm text-gray-500 truncate mb-4" title={server.url}>{server.url}</p>
            <div class="flex gap-2">
              <button
                on:click={() => openServer(server.url)}
                class="flex-1 px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 text-sm flex items-center justify-center"
              >
                <svg class="w-4 h-4 mr-2" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
                </svg>
                Open
              </button>
              <button
                on:click={() => { navigator.clipboard.writeText(server.url); alert('URL copied!'); }}
                class="px-4 py-2 border border-gray-300 rounded-lg hover:bg-gray-50 text-sm"
                title="Copy URL"
              >
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
                </svg>
              </button>
            </div>
          </div>
          <div class="px-6 py-3 bg-gray-50 rounded-b-lg border-t">
            <p class="text-xs text-gray-500">
              Access granted {new Date(server.grantedAt).toLocaleDateString()}
            </p>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</DashboardLayout>
