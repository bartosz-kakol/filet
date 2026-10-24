<script lang="ts">
	import { session } from '$lib/stores/session.svelte';
	import LoginForm from './LoginForm.svelte';
	import Modal from './Modal.svelte';
</script>

<Modal
	bind:open={session.reloginOpen}
	title="Sign in to continue"
	description="Your session expired. Transfers are paused and continue after you sign in."
>
	<LoginForm onSuccess={() => session.reloggedIn()} />
</Modal>
