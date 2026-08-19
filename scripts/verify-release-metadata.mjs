import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { readFile, stat } from 'node:fs/promises';
import { basename, dirname, join } from 'node:path';

const publicBase = (process.env.CINEKO_RELEASES_PUBLIC_BASE_URL ?? '').replace(/\/$/, '');
if (!publicBase.startsWith('https://')) throw new Error('CINEKO_RELEASES_PUBLIC_BASE_URL must be an HTTPS origin or prefix');

for (const metadataPath of process.argv.slice(2)) {
  const metadata = JSON.parse(await readFile(metadataPath, 'utf8'));
  const { component, objectKey, file, release } = metadata;
  if (!['client', 'playwright'].includes(component) || basename(file) !== file || !objectKey.endsWith(`/${file}`)) throw new Error('invalid release identity');
  const artifactPath = join(dirname(metadataPath), file);
  const info = await stat(artifactPath);
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(artifactPath)) hash.update(chunk);
  if (release.artifact.size !== info.size || release.artifact.sha256 !== hash.digest('hex') || !release.artifact.url.startsWith(`${publicBase}/`)) throw new Error('artifact metadata mismatch');
  if (new Date(release.publishedAt).toISOString() !== release.publishedAt) throw new Error('release timestamp is not canonical UTC');
  if (release.platform === 'darwin' && release.arch !== 'arm64') throw new Error('unsupported Darwin architecture');
  if (component === 'client' && (!release.minimumLauncherVersion || !release.minimumBrowserRevision || !release.playwrightVersion || release.protocol !== 3 || Object.keys(release.probeBootstrapPublicKeys).length === 0)) throw new Error('incomplete Client release');
}
