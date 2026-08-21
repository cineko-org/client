import { execFileSync } from 'node:child_process';

if (process.argv.length < 3) throw new Error('release metadata paths are required');
execFileSync('go', [
  'run', '-mod=vendor', './cmd/releasecontract', 'verify-release', ...process.argv.slice(2),
], { stdio: 'inherit', env: { ...process.env, GOWORK: 'off' } });
