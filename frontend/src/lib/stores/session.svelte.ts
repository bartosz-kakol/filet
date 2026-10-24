import { get } from '$lib/api/client';

class Session {
	authenticated = $state<boolean | null>(null);
	/** Shown as a modal while uploads or jobs are active (401 mid-transfer). */
	reloginOpen = $state(false);
	#onRelogin: (() => void)[] = [];

	async check(): Promise<boolean> {
		try {
			const s = await get<{ authenticated: boolean }>('/api/auth/session', { noAuthRedirect: true });
			this.authenticated = s.authenticated;
		} catch {
			this.authenticated = false;
		}
		return this.authenticated;
	}

	onRelogin(fn: () => void) {
		this.#onRelogin.push(fn);
	}

	reloggedIn() {
		this.authenticated = true;
		this.reloginOpen = false;
		this.#onRelogin.forEach((f) => f());
	}
}

export const session = new Session();
