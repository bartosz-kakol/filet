import {
	isBool,
	isConcurrency,
	isMarkdownMode,
	isSort,
	isString,
	isTheme,
	readPref,
	writePref,
	type Sort,
	type Theme
} from './storage';

function persisted<T>(key: string, def: T, valid: (v: unknown) => v is T) {
	let value = $state(readPref(key, def, valid));
	return {
		get value() {
			return value;
		},
		set value(v: T) {
			value = v;
			writePref(key, v);
		}
	};
}

class Prefs {
	#theme = persisted<Theme>('filet.theme', 'system', isTheme);
	#sort = persisted<Sort>('filet.sort', { key: 'name', dir: 'asc' }, isSort);
	#showHidden = persisted('filet.showHidden', false, isBool);
	#concurrency = persisted('filet.uploadConcurrency', 4, isConcurrency);
	#panelCollapsed = persisted('filet.panelCollapsed', false, isBool);
	#lastPlace = persisted<string>('filet.lastPlace', '', isString);
	#softWrap = persisted('filet.softWrap', true, isBool);
	#markdownMode = persisted<'rendered' | 'raw'>('filet.markdownMode', 'rendered', isMarkdownMode);

	get theme() {
		return this.#theme.value;
	}
	set theme(v: Theme) {
		this.#theme.value = v;
		(window as unknown as { __filetApplyTheme?: () => void }).__filetApplyTheme?.();
	}
	get sort() {
		return this.#sort.value;
	}
	set sort(v: Sort) {
		this.#sort.value = v;
	}
	get showHidden() {
		return this.#showHidden.value;
	}
	set showHidden(v: boolean) {
		this.#showHidden.value = v;
	}
	get uploadConcurrency() {
		return this.#concurrency.value;
	}
	set uploadConcurrency(v: number) {
		this.#concurrency.value = Math.min(16, Math.max(1, Math.round(v)));
	}
	get panelCollapsed() {
		return this.#panelCollapsed.value;
	}
	set panelCollapsed(v: boolean) {
		this.#panelCollapsed.value = v;
	}
	get lastPlace() {
		return this.#lastPlace.value;
	}
	set lastPlace(v: string) {
		this.#lastPlace.value = v;
	}
	get softWrap() {
		return this.#softWrap.value;
	}
	set softWrap(v: boolean) {
		this.#softWrap.value = v;
	}
	get markdownMode() {
		return this.#markdownMode.value;
	}
	set markdownMode(v: 'rendered' | 'raw') {
		this.#markdownMode.value = v;
	}
}

export const prefs = new Prefs();
