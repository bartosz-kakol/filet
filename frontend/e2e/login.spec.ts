import { PASS, USER, expect, test } from './fixtures';

test('login: wrong password, lockout countdown, success, logout', async ({ page, isMobile }) => {
	await page.goto('/');
	await expect(page).toHaveURL(/\/login\?next=/);
	await page.getByLabel('Username').fill(USER);
	await page.getByLabel('Password', { exact: true }).fill('wrong');
	await page.getByRole('button', { name: 'Sign in' }).click();
	await expect(page.getByRole('alert')).toHaveText('Incorrect username or password');
	// A second attempt during the lock shows the countdown.
	await page.getByLabel('Password', { exact: true }).fill(PASS);
	await page.getByRole('button', { name: 'Sign in' }).click();
	await expect(page.getByRole('button', { name: /Try again in \d s/ })).toBeDisabled();
	await expect(page.getByRole('button', { name: 'Sign in' })).toBeEnabled({ timeout: 5000 });
	await page.getByRole('button', { name: 'Sign in' }).click();
	await expect(page).toHaveURL(/\/p\/media$/);
	await page.getByTestId('app-menu').click();
	await page.getByRole('menuitem', { name: 'Sign out' }).click();
	await expect(page).toHaveURL(/\/login/);
	void isMobile;
});
