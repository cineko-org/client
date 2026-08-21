import { execFileSync } from 'node:child_process';
import { basename } from 'node:path';
import { writeFile } from 'node:fs/promises';

if (process.argv.length !== 7) throw new Error('expected component, version, platform, artifact, and executable');
const [component, rawVersion, platform, artifact, executable] = process.argv.slice(2);
if (!['client', 'playwright'].includes(component)) throw new Error('unsupported component');
const version = rawVersion.replace(/^v/, '');
const platformSlug = platform.replace('/', '-');
const publicBase = (process.env.CINEKO_RELEASES_PUBLIC_BASE_URL ?? '').replace(/\/$/, '');
const publishedAt = process.env.CINEKO_RELEASE_PUBLISHED_AT ?? '';
const publicUrl = `${publicBase}/${component}/v${version}/${platformSlug}/${basename(artifact)}`;
const payload = execFileSync('go', [
  'run', '-mod=vendor', './cmd/releasecontract', 'release',
  component, version, platform, artifact, executable, publicUrl, publishedAt,
], { encoding: 'utf8', env: { ...process.env, GOWORK: 'off' } });
await writeFile(`build/release/${component}-release-${platformSlug}.json`, payload);
