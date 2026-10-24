<script lang="ts">
	import { page } from '$app/state';
</script>

<div class="flex h-dvh flex-col items-center justify-center gap-3 p-6 text-center">
	<h1 class="text-2xl font-semibold">{page.status === 404 ? 'Not found' : 'Something went wrong'}</h1>
	<p class="text-muted">{page.status === 404 ? 'There is nothing at this address.' : page.error?.message}</p>
	<a class="btn" href="/">Go home</a>
</div>
