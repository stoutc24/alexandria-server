<script>
  import { auth } from '$lib/auth.js';
  import { api } from '$lib/api.js';
  import { goto } from '$app/navigation';
  import { base } from '$app/paths';
  import { onMount } from 'svelte';

  let email = '';
  let password = '';
  let error = '';
  let loading = false;
  let isRegister = false;
  let username = '';

  onMount(() => {
    const unsubscribe = auth.subscribe(state => {
      if (!state.isLoading && state.isAuthenticated) {
        goto(`${base}/dashboard`);
      }
    });
    return unsubscribe;
  });

  async function handleSubmit() {
    error = '';
    loading = true;

    try {
      let data;
      if (isRegister) {
        data = await api.register(username, email, password);
      } else {
        data = await api.login(email, password);
      }
      auth.login(data.accessToken, data.refreshToken, data.user);
      goto(`${base}/dashboard`);
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  function toggleMode() {
    isRegister = !isRegister;
    error = '';
  }
</script>

<div class="min-h-screen flex items-center justify-center bg-gradient-to-br from-primary-600 to-primary-800">
  <div class="bg-white rounded-lg shadow-xl p-8 w-full max-w-md">
    <div class="text-center mb-8">
      <h1 class="text-3xl font-bold text-gray-900">Alexandria</h1>
      <p class="text-gray-600 mt-2">{isRegister ? 'Create your account' : 'Sign in to admin panel'}</p>
    </div>

    {#if error}
      <div class="bg-red-50 text-red-600 p-3 rounded-lg mb-4 text-sm">
        {error}
      </div>
    {/if}

    <form on:submit|preventDefault={handleSubmit} class="space-y-4">
      {#if isRegister}
        <div>
          <label for="username" class="block text-sm font-medium text-gray-700 mb-1">Username</label>
          <input
            id="username"
            type="text"
            bind:value={username}
            required
            class="w-full px-4 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-primary-500 focus:border-transparent"
            placeholder="Enter your username"
          />
        </div>
      {/if}

      <div>
        <label for="email" class="block text-sm font-medium text-gray-700 mb-1">Email</label>
        <input
          id="email"
          type="email"
          bind:value={email}
          required
          class="w-full px-4 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-primary-500 focus:border-transparent"
          placeholder="Enter your email"
        />
      </div>

      <div>
        <label for="password" class="block text-sm font-medium text-gray-700 mb-1">Password</label>
        <input
          id="password"
          type="password"
          bind:value={password}
          required
          class="w-full px-4 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-primary-500 focus:border-transparent"
          placeholder="Enter your password"
        />
      </div>

      <button
        type="submit"
        disabled={loading}
        class="w-full bg-primary-600 text-white py-2 px-4 rounded-lg hover:bg-primary-700 focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
      >
        {#if loading}
          <span class="inline-flex items-center">
            <svg class="animate-spin -ml-1 mr-2 h-4 w-4 text-white" fill="none" viewBox="0 0 24 24">
              <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
              <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
            </svg>
            {isRegister ? 'Creating account...' : 'Signing in...'}
          </span>
        {:else}
          {isRegister ? 'Create Account' : 'Sign In'}
        {/if}
      </button>
    </form>

    <div class="mt-6 text-center">
      <button
        on:click={toggleMode}
        class="text-primary-600 hover:text-primary-700 text-sm"
      >
        {isRegister ? 'Already have an account? Sign in' : "Don't have an account? Register"}
      </button>
    </div>
  </div>
</div>
