<script>
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { base } from '$app/paths';
  import { auth } from '$lib/auth.js';
  import { api } from '$lib/api.js';
  import DashboardLayout from '$lib/DashboardLayout.svelte';

  let families = [];
  let loading = true;
  let error = '';
  let showCreateModal = false;
  let showJoinModal = false;
  let newFamilyName = '';
  let inviteCode = '';
  let modalError = '';
  let modalLoading = false;

  onMount(() => {
    const unsubscribe = auth.subscribe(state => {
      if (!state.isLoading && !state.isAuthenticated) {
        goto(base || '/admin');
      }
    });

    loadFamilies();
    return unsubscribe;
  });

  async function loadFamilies() {
    try {
      families = await api.getFamilies() || [];
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  async function createFamily() {
    modalError = '';
    modalLoading = true;
    try {
      await api.createFamily(newFamilyName);
      showCreateModal = false;
      newFamilyName = '';
      await loadFamilies();
    } catch (e) {
      modalError = e.message;
    } finally {
      modalLoading = false;
    }
  }

  async function joinFamily() {
    modalError = '';
    modalLoading = true;
    try {
      await api.joinFamily(inviteCode);
      showJoinModal = false;
      inviteCode = '';
      await loadFamilies();
    } catch (e) {
      modalError = e.message;
    } finally {
      modalLoading = false;
    }
  }

  function closeModals() {
    showCreateModal = false;
    showJoinModal = false;
    newFamilyName = '';
    inviteCode = '';
    modalError = '';
  }
</script>

<DashboardLayout title="Families">
  <div class="mb-6 flex gap-4">
    <button
      on:click={() => showCreateModal = true}
      class="bg-primary-600 text-white px-4 py-2 rounded-lg hover:bg-primary-700 transition-colors flex items-center"
    >
      <svg class="w-5 h-5 mr-2" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
      </svg>
      Create Family
    </button>
    <button
      on:click={() => showJoinModal = true}
      class="bg-white text-gray-700 px-4 py-2 rounded-lg border hover:bg-gray-50 transition-colors flex items-center"
    >
      <svg class="w-5 h-5 mr-2" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M18 9v3m0 0v3m0-3h3m-3 0h-3m-2-5a4 4 0 11-8 0 4 4 0 018 0zM3 20a6 6 0 0112 0v1H3v-1z" />
      </svg>
      Join Family
    </button>
  </div>

  {#if loading}
    <div class="flex items-center justify-center h-64">
      <div class="animate-spin rounded-full h-8 w-8 border-b-2 border-primary-600"></div>
    </div>
  {:else if error}
    <div class="bg-red-50 text-red-600 p-4 rounded-lg">{error}</div>
  {:else if families.length === 0}
    <div class="bg-white rounded-lg shadow p-12 text-center">
      <svg class="w-16 h-16 mx-auto text-gray-300 mb-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z" />
      </svg>
      <h3 class="text-lg font-medium text-gray-900 mb-2">No families yet</h3>
      <p class="text-gray-500">Create a new family or join an existing one with an invite code.</p>
    </div>
  {:else}
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
      {#each families as family}
        <a
          href="{base}/families/{family.id}"
          class="bg-white rounded-lg shadow hover:shadow-lg transition-shadow p-6"
        >
          <div class="flex items-center mb-4">
            <div class="w-12 h-12 bg-primary-100 rounded-lg flex items-center justify-center">
              <svg class="w-6 h-6 text-primary-600" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z" />
              </svg>
            </div>
          </div>
          <h3 class="text-lg font-semibold text-gray-900 mb-1">{family.name}</h3>
          <p class="text-sm text-gray-500">
            Created {new Date(family.createdAt).toLocaleDateString()}
          </p>
        </a>
      {/each}
    </div>
  {/if}
</DashboardLayout>

<!-- Create Family Modal -->
{#if showCreateModal}
  <div class="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
    <div class="bg-white rounded-lg shadow-xl p-6 w-full max-w-md">
      <h3 class="text-lg font-semibold text-gray-900 mb-4">Create New Family</h3>

      {#if modalError}
        <div class="bg-red-50 text-red-600 p-3 rounded-lg mb-4 text-sm">{modalError}</div>
      {/if}

      <form on:submit|preventDefault={createFamily}>
        <div class="mb-4">
          <label for="familyName" class="block text-sm font-medium text-gray-700 mb-1">Family Name</label>
          <input
            id="familyName"
            type="text"
            bind:value={newFamilyName}
            required
            class="w-full px-4 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-primary-500 focus:border-transparent"
            placeholder="Enter family name"
          />
        </div>

        <div class="flex gap-3">
          <button
            type="button"
            on:click={closeModals}
            class="flex-1 px-4 py-2 border border-gray-300 rounded-lg text-gray-700 hover:bg-gray-50"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={modalLoading}
            class="flex-1 px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 disabled:opacity-50"
          >
            {modalLoading ? 'Creating...' : 'Create'}
          </button>
        </div>
      </form>
    </div>
  </div>
{/if}

<!-- Join Family Modal -->
{#if showJoinModal}
  <div class="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
    <div class="bg-white rounded-lg shadow-xl p-6 w-full max-w-md">
      <h3 class="text-lg font-semibold text-gray-900 mb-4">Join Family</h3>

      {#if modalError}
        <div class="bg-red-50 text-red-600 p-3 rounded-lg mb-4 text-sm">{modalError}</div>
      {/if}

      <form on:submit|preventDefault={joinFamily}>
        <div class="mb-4">
          <label for="inviteCode" class="block text-sm font-medium text-gray-700 mb-1">Invite Code</label>
          <input
            id="inviteCode"
            type="text"
            bind:value={inviteCode}
            required
            class="w-full px-4 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-primary-500 focus:border-transparent font-mono"
            placeholder="Enter invite code"
          />
        </div>

        <div class="flex gap-3">
          <button
            type="button"
            on:click={closeModals}
            class="flex-1 px-4 py-2 border border-gray-300 rounded-lg text-gray-700 hover:bg-gray-50"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={modalLoading}
            class="flex-1 px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 disabled:opacity-50"
          >
            {modalLoading ? 'Joining...' : 'Join'}
          </button>
        </div>
      </form>
    </div>
  </div>
{/if}
