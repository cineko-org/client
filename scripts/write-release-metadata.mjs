import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { stat, writeFile } from 'node:fs/promises';
import { basename } from 'node:path';

if (process.argv.length !== 7) throw new Error('expected component, version, platform, artifact, and executable');
const [component, rawVersion, rawPlatform, file, executable] = process.argv.slice(2);
if (!['client', 'playwright'].includes(component)) throw new Error('unsupported component');
const [platform, arch, extra] = rawPlatform.split('/');
if (extra || !['darwin', 'windows', 'linux'].includes(platform) || !['arm64', 'amd64'].includes(arch)) {
  throw new Error('unsupported platform');
}
const version = rawVersion.replace(/^v/, '');
const stableVersion = /^[0-9]+\.[0-9]+\.[0-9]+$/;
if (!stableVersion.test(version)) throw new Error('invalid stable component version');
const directoryVersion = `v${version}`;
const platformSlug = `${platform}-${arch}`;
const publicBase = (process.env.CINEKO_RELEASES_PUBLIC_BASE_URL ?? '').replace(/\/$/, '');
if (!publicBase.startsWith('https://')) throw new Error('CINEKO_RELEASES_PUBLIC_BASE_URL must be an HTTPS origin or prefix');
const publishedAtInput = process.env.CINEKO_RELEASE_PUBLISHED_AT ?? '';
const publishedAtDate = new Date(publishedAtInput);
if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(publishedAtInput) || Number.isNaN(publishedAtDate.valueOf())) {
  throw new Error('CINEKO_RELEASE_PUBLISHED_AT must be a UTC RFC3339 timestamp');
}
const publishedAt = publishedAtDate.toISOString();
const info = await stat(file);
const hash = createHash('sha256');
for await (const chunk of createReadStream(file)) hash.update(chunk);
const artifact = {
  url: `${publicBase}/${component}/${directoryVersion}/${platformSlug}/${basename(file)}`,
  size: info.size,
  sha256: hash.digest('hex'),
  executable,
};
const common = { channel: 'stable', platform, arch, artifact, publishedAt };
let release;
if (component === 'client') {
  const minimumLauncherVersion = (process.env.CINEKO_MINIMUM_LAUNCHER_VERSION ?? '').replace(/^v/, '');
  const playwrightVersion = (process.env.CINEKO_PLAYWRIGHT_VERSION ?? '').replace(/^v/, '');
  const minimumBrowserRevision = process.env.CINEKO_BROWSER_REVISION ?? '';
  if (!stableVersion.test(minimumLauncherVersion) || !stableVersion.test(playwrightVersion) || !/^[1-9][0-9]*$/.test(minimumBrowserRevision)) throw new Error('invalid Client compatibility');
  let probeBootstrapPublicKeys;
  try {
    probeBootstrapPublicKeys = JSON.parse(process.env.CINEKO_PROBE_BOOTSTRAP_PUBLIC_KEYS_JSON ?? '');
  } catch {
    throw new Error('CINEKO_PROBE_BOOTSTRAP_PUBLIC_KEYS_JSON must be a valid JSON object');
  }
  if (!probeBootstrapPublicKeys || Array.isArray(probeBootstrapPublicKeys) ||
      Object.entries(probeBootstrapPublicKeys).length === 0 ||
      Object.entries(probeBootstrapPublicKeys).some(([key, value]) => !key.trim() || typeof value !== 'string' || !value.trim())) {
    throw new Error('CINEKO_PROBE_BOOTSTRAP_PUBLIC_KEYS_JSON must contain at least one named public key');
  }
  release = {
    ...common,
    version,
    minimumLauncherVersion,
    minimumBrowserRevision,
    playwrightVersion,
    protocol: Number(process.env.CINEKO_PROTOCOL_VERSION ?? '3'),
    probeBootstrapPublicKeys,
  };
} else {
  release = { ...common, version };
}
const metadata = { component, objectKey: `${component}/${directoryVersion}/${platformSlug}/${basename(file)}`, file: basename(file), release };
await writeFile(`build/release/${component}-release-${platformSlug}.json`, `${JSON.stringify(metadata, null, 2)}\n`);
