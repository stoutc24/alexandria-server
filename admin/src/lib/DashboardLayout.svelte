<script>
  import { auth } from '$lib/auth.js';
  import { page } from '$app/stores';
  import { base } from '$app/paths';

  export let title = 'Dashboard';

  $: currentPath = $page.url.pathname;
  $: user = $auth.user;

  const navItems = [
    { href: `${base}/dashboard`, label: 'Dashboard', icon: 'home' },
    { href: `${base}/families`, label: 'Families', icon: 'users' },
    { href: `${base}/servers`, label: 'My Servers', icon: 'server' }
  ];

  function isActive(href) {
    if (href === `${base}/dashboard`) {
      return currentPath === href;
    }
    return currentPath.startsWith(href);
  }
</script>

<div class="min-h-screen bg-gray-50">
  <!-- Sidebar -->
  <aside class="fixed inset-y-0 left-0 w-64 bg-white shadow-lg">
    <div class="flex flex-col h-full">
      <!-- Logo -->
      <div class="p-6 border-b">
        <h1 class="text-xl font-bold text-primary-600">Alexandria</h1>
        <p class="text-sm text-gray-500">Admin Panel</p>
      </div>

      <!-- Navigation -->
      <nav class="flex-1 p-4 space-y-1">
        {#each navItems as item}
          <a
            href={item.href}
            class="flex items-center px-4 py-3 rounded-lg transition-colors {isActive(item.href)
              ? 'bg-primary-50 text-primary-700'
              : 'text-gray-600 hover:bg-gray-100'}"
          >
            {#if item.icon === 'home'}
              <svg class="w-5 h-5 mr-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6" />
              </svg>
            {:else if item.icon === 'users'}
              <svg class="w-5 h-5 mr-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z" />
              </svg>
            {:else if item.icon === 'server'}
              <svg class="w-5 h-5 mr-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-2-4h.01M17 16h.01" />
              </svg>
            {/if}
            {item.label}
          </a>
        {/each}
      </nav>

      <!-- User section -->
      <div class="p-4 border-t">
        <div class="flex items-center">
          <div class="w-10 h-10 bg-primary-100 rounded-full flex items-center justify-center">
            <span class="text-primary-600 font-semibold">
              {user?.username?.charAt(0).toUpperCase() || 'U'}
            </span>
          </div>
          <div class="ml-3 flex-1">
            <p class="text-sm font-medium text-gray-700">{user?.username || 'User'}</p>
            <p class="text-xs text-gray-500">{user?.email || ''}</p>
          </div>
        </div>
        <button
          on:click={() => auth.logout()}
          class="mt-4 w-full text-left px-4 py-2 text-sm text-red-600 hover:bg-red-50 rounded-lg transition-colors"
        >
          Sign out
        </button>
      </div>
    </div>
  </aside>

  <!-- Main content -->
  <main class="ml-64 p-8">
    <div class="mb-8">
      <h2 class="text-2xl font-bold text-gray-900">{title}</h2>
    </div>
    <slot />
  </main>
</div>
