<script>
  import '../app.css';
  import { auth } from '$lib/auth.js';
  import { onMount } from 'svelte';

  onMount(() => {
    auth.init();
  });
</script>

<slot />
