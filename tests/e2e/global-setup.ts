// Creates a fresh super administrator for this run with itkctl (the same
// bootstrap path the installer uses) and stores its credentials for tests.
import { execFileSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

export default async function globalSetup() {
	const root = resolve(import.meta.dirname, '..', '..');
	const run = randomBytes(3).toString('hex');
	const admin = { username: `e2eadmin${run}`, password: `E2e-Admin-${randomBytes(6).toString('hex')}!`, run };
	const args = ['admin', 'create', '--username', admin.username, '--email', `${admin.username}@itk.test`, '--name', 'E2E Administrator', '--password-stdin'];
	// E2E_ITKCTL: command that runs itkctl on an installed server (e.g. a
	// wrapper around `ssh host sudo itkctl`); it receives the itkctl arguments
	// and the password on stdin. Otherwise use the local development build.
	const remote = process.env.E2E_ITKCTL;
	if (remote) {
		execFileSync(remote, args, { input: admin.password + '\n', stdio: ['pipe', 'inherit', 'inherit'] });
	} else {
		const itkctl = resolve(root, 'bin', 'itkctl');
		if (!existsSync(itkctl)) throw new Error('bin/itkctl not found: run `make build-go` first');
		const env = { ...process.env, ITK_CLOUD_ENV_FILE: process.env.ITK_CLOUD_ENV_FILE ?? resolve(root, '.env') };
		execFileSync(itkctl, args, { input: admin.password + '\n', env, cwd: root, stdio: ['pipe', 'inherit', 'inherit'] });
	}
	const dir = resolve(root, '.dev', 'e2e');
	mkdirSync(dir, { recursive: true });
	writeFileSync(resolve(dir, 'e2e-admin.json'), JSON.stringify(admin));
}
