<script>
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { base } from '$app/paths';
  import { auth } from '$lib/auth.js';
  import { api } from '$lib/api.js';
  import DashboardLayout from '$lib/DashboardLayout.svelte';

  let families = [];
  let servers = [];
  let loading = true;
  let error = '';

  onMount(() => {
    const unsubscribe = auth.subscribe(state => {
      if (!state.isLoading && !state.isAuthenticated) {
        goto(base || '/admin');
      }
    });

    loadData();
    return unsubscribe;
  });

  async function loadData() {
    try {
      const [familiesData, serversData] = await Promise.all([
        api.getFamilies(),
        api.getMyServers()
      ]);
      families = familiesData || [];
      servers = serversData || [];
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }
</script>

<DashboardLayout title="Dashboard">
  {#if loading}
    <div class="flex items-center justify-center h-64">
      <div class="animate-spin rounded-full h-8 w-8 border-b-2 border-primary-600"></div>
    </div>
  {:else if error}
    <div class="bg-red-50 text-red-600 p-4 rounded-lg">{error}</div>
  {:else}
    <div class="grid grid-cols-1 md:grid-cols-3 gap-6 mb-8">
      <!-- Families Card -->
      <div class="bg-white rounded-lg shadow p-6">
        <div class="flex items-center">
          <div class="p-3 bg-blue-100 rounded-lg">
            <svg class="w-6 h-6 text-blue-600" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z" />
            </svg>
          </div>
          <div class="ml-4">
            <p class="text-sm text-gray-500">Families</p>
            <p class="text-2xl font-bold text-gray-900">{families.length}</p>
          </div>
        </div>
      </div>

      <!-- Servers Card -->
      <div class="bg-white rounded-lg shadow p-6">
        <div class="flex items-center">
          <div class="p-3 bg-green-100 rounded-lg">
            <svg class="w-6 h-6 text-green-600" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-2-4h.01M17 16h.01" />
            </svg>
          </div>
          <div class="ml-4">
            <p class="text-sm text-gray-500">My Servers</p>
            <p class="text-2xl font-bold text-gray-900">{servers.length}</p>
          </div>
        </div>
      </div>

      <!-- Quick Actions Card -->
      <div class="bg-white rounded-lg shadow p-6">
        <div class="flex items-center">
          <div class="p-3 bg-purple-100 rounded-lg">
            <svg class="w-6 h-6 text-purple-600" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z" />
            </svg>
          </div>
          <div class="ml-4">
            <p class="text-sm text-gray-500">Quick Actions</p>
            <a href="{base}/families" class="text-primary-600 hover:text-primary-700 text-sm font-medium">
              Manage Families →
            </a>
          </div>
        </div>
      </div>
    </div>

    <!-- Recent Families -->
    <div class="bg-white rounded-lg shadow">
      <div class="px-6 py-4 border-b">
        <h3 class="text-lg font-semibold text-gray-900">Your Families</h3>
      </div>
      <div class="p-6">
        {#if families.length === 0}
          <p class="text-gray-500 text-center py-8">No families yet. Create or join one to get started.</p>
        {:else}
          <div class="space-y-4">
            {#each families as family}
              <a
                href="{base}/families/{family.id}"
                class="block p-4 border rounded-lg hover:border-primary-300 hover:bg-primary-50 transition-colors"
              >
                <div class="flex items-center justify-between">
                  <div>
                    <h4 class="font-medium text-gray-900">{family.name}</h4>
                    <p class="text-sm text-gray-500">Created {new Date(family.createdAt).toLocaleDateString()}</p>
                  </div>
                  <svg class="w-5 h-5 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
                  </svg>
                </div>
              </a>
            {/each}
          </div>
        {/if}
      </div>
    </div>
  {/if}
</DashboardLayout>
