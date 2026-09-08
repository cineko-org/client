import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { chmod, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';

process.env.CINEKO_RELEASES_PUBLIC_BASE_URL = 'https://releases.example.com/cineko';
const goModuleCache = execFileSync('go', ['env', 'GOMODCACHE'], { encoding: 'utf8' }).trim();

test('GitHub registration and runtime manifests use flat release asset URLs', async () => {
  const root = await mkdtemp(join(tmpdir(), 'cineko-release-registration-'));
  const clientVersion = '2.8.1';
  const playwrightVersion = execFileSync('bash', ['scripts/playwright-version.sh', 'driver'], { encoding: 'utf8' }).trim();
  const filenames = [
    `cineko-client-v${clientVersion}-darwin-arm64.zip`,
    `cineko-client-v${clientVersion}-windows-amd64.zip`,
    `cineko-client-v${clientVersion}-linux-amd64.tar.gz`,
    `cineko-playwright-${playwrightVersion}-darwin-arm64.tar.gz`,
    `cineko-playwright-${playwrightVersion}-windows-amd64.zip`,
    `cineko-playwright-${playwrightVersion}-linux-amd64.tar.gz`,
  ];
  try {
    await Promise.all(filenames.map((filename) => writeFile(join(root, filename), filename)));
    const clientSet = JSON.parse(execFileSync('bash', [
      'scripts/register-client-release.sh', clientVersion, '2026-08-24T00:00:00Z', root,
    ], {
      encoding: 'utf8',
      env: {
        ...process.env,
        CINEKO_CLIENT_RELEASE_BASE: `https://github.com/cineko-org/client/releases/download/v${clientVersion}`,
        CINEKO_MINIMUM_LAUNCHER_VERSION: '1.4.0',
        CINEKO_BROWSER_REVISION: '1228',
        CINEKO_PLAYWRIGHT_VERSION: playwrightVersion,
      },
    }));
    const playwrightSet = JSON.parse(execFileSync('bash', [
      'scripts/register-playwright-release.sh', playwrightVersion, '2026-08-24T00:00:00Z', root,
    ], {
      encoding: 'utf8',
      env: {
        ...process.env,
        CINEKO_PLAYWRIGHT_RELEASE_BASE: `https://github.com/cineko-org/client/releases/download/playwright-v${playwrightVersion}`,
      },
    }));
    const browserSet = {
      releases: clientSet.releases.map(({ platform, architecture, publishedAt, artifact }) => ({
        platform, architecture, publishedAt, artifact,
        channel: 'stable', revision: '1228', compatiblePlaywrightVersions: [playwrightVersion],
      })),
    };
    const setPaths = ['client', 'browser', 'playwright'].map((name) => join(root, name + '-set.json'));
    await Promise.all([clientSet, browserSet, playwrightSet].map((value, index) =>
      writeFile(setPaths[index], JSON.stringify(value))));
    execFileSync('bash', ['scripts/publish-runtime-channel.sh', ...setPaths, root]);
    for (const client of clientSet.releases) {
      const path = join(root, 'runtime-' + client.platform + '-' + client.architecture + '.json');
      const manifest = JSON.parse(await readFile(path, 'utf8'));
      assert.deepEqual(manifest.client, client);
      assert.equal(manifest.playwright.version, playwrightVersion);
      assert.equal(manifest.browser.revision, '1228');
    }
    browserSet.releases.pop();
    await writeFile(setPaths[1], JSON.stringify(browserSet));
    assert.throws(() => execFileSync('bash', ['scripts/publish-runtime-channel.sh', ...setPaths, root], { stdio: 'pipe' }));
    for (const release of [...clientSet.releases, ...playwrightSet.releases]) {
      const platformKey = `${release.platform}-${release.architecture}`;
      assert.equal(new URL(release.artifact.url).pathname.includes(`/${platformKey}/`), false);
      assert.match(release.artifact.url, /^https:\/\/github\.com\/cineko-org\/client\/releases\/download\/[^/]+\/[^/]+$/);
    }
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('writes independently publishable component metadata', async () => {
  await mkdir('build/release', { recursive: true });
  const fixtures = [
    ['client', '2.3.4', 'cineko-client-v2.3.4-linux-amd64.tar.gz'],
    ['playwright', '1.61.1', 'cineko-playwright-1.61.1-linux-amd64.tar.gz'],
  ];
  const outputs = fixtures.map(([component]) => `build/release/${component}-release-linux-amd64.json`);
  try {
    for (const [component, version, filename] of fixtures) {
      const path = `build/release/${filename}`;
      await writeFile(path, `${component}-fixture`);
      execFileSync('node', ['scripts/write-release-metadata.mjs', component, version, 'linux/amd64', path, 'bin/executable'], {
        env: {
          ...process.env,
          CINEKO_MINIMUM_LAUNCHER_VERSION: '1.0.0',
          CINEKO_BROWSER_REVISION: '1228',
          CINEKO_PLAYWRIGHT_VERSION: '1.61.1',
          CINEKO_RELEASE_PUBLISHED_AT: '2026-08-12T09:00:00Z',
        },
      });
    }
    execFileSync('node', ['scripts/verify-release-metadata.mjs', ...outputs]);
    const client = JSON.parse(await readFile(outputs[0], 'utf8'));
    assert.equal(client.artifact.url, 'https://releases.example.com/cineko/cineko-client-v2.3.4-linux-amd64.tar.gz');
    assert.equal(client.playwrightVersion, '1.61.1');
    assert.equal(client.architecture, 'amd64');
  } finally {
    await Promise.all([...fixtures.map(([, , filename]) => rm(`build/release/${filename}`, { force: true })), ...outputs.map((path) => rm(path, { force: true }))]);
  }
});

test('browser publication verifies GitHub digests, resumes drafts and never replaces published bytes', async () => {
  const root = await mkdtemp(join(tmpdir(), 'cineko-github-browser-test-'));
  const tools = join(root, 'bin');
  const assets = join(root, 'archives');
  const statePath = join(root, 'github.json');
  try {
    await Promise.all([mkdir(tools), mkdir(assets)]);
    const filenames = ['chrome-mac-arm64.zip', 'chrome-linux64.zip', 'chrome-win64.zip'];
    await Promise.all(filenames.map((filename) => writeFile(join(assets, filename), filename)));
    const gh = join(tools, 'gh');
    await writeFile(gh, `#!/usr/bin/env node
const fs = require('node:fs');
const { basename } = require('node:path');
const { createHash } = require('node:crypto');
const path = process.env.FAKE_GITHUB_STATE;
const args = process.argv.slice(2);
let state = fs.existsSync(path) ? JSON.parse(fs.readFileSync(path, 'utf8')) : null;
if (args[0] === 'api') {
  if (!state) process.exit(1);
  if (args[1] !== 'repos/cineko-org/client/releases/123') process.exit(1);
  process.stdout.write(JSON.stringify(state));
} else if (args[1] === 'view') {
  if (!state) process.exit(1);
  if (args.includes('--json')) process.stdout.write('123');
} else if (args[1] === 'create') {
  if (!args.includes('--draft') || !args.includes('--latest=false')) process.exit(2);
  state = { draft: true, assets: [], uploads: 0 };
} else if (args[1] === 'upload') {
  if (!state.draft || args.includes('--clobber')) process.exit(2);
  const name = basename(args[3]);
  if (state.assets.some((asset) => asset.name === name)) process.exit(2);
  const contents = fs.readFileSync(args[3]);
  state.assets.push({ name, size: contents.length, digest: 'sha256:' + createHash('sha256').update(contents).digest('hex') });
  state.uploads += 1;
} else if (args[1] === 'edit') {
  if (state.assets.length !== 3 || !args.includes('--latest=false')) process.exit(2);
  state.draft = false;
} else {
  process.exit(2);
}
fs.writeFileSync(path, JSON.stringify(state));
`);
    await chmod(gh, 0o755);
    const env = {
      ...process.env, PATH: `${tools}:${process.env.PATH}`, FAKE_GITHUB_STATE: statePath,
      GITHUB_REPOSITORY: 'cineko-org/client', CINEKO_RELEASE_TARGET_SHA: '0123456789abcdef',
    };
    const publish = () => execFileSync('bash', ['scripts/publish-browser-assets.sh', '149.0.7827.55', assets], { env, stdio: 'pipe' });
    publish();
    publish();
    const state = JSON.parse(await readFile(statePath, 'utf8'));
    assert.equal(state.uploads, 3, 'second publication must not upload any bytes');
    assert.equal(state.draft, false);
    const missing = state.assets.pop();
    await writeFile(statePath, JSON.stringify(state));
    assert.throws(publish, 'incomplete public release must not be silently modified');
    state.draft = true;
    await writeFile(statePath, JSON.stringify(state));
    publish();
    const resumed = JSON.parse(await readFile(statePath, 'utf8'));
    assert.equal(resumed.uploads, 4, 'draft resumption only uploads the missing asset');
    assert.equal(resumed.draft, false);
    await writeFile(join(assets, missing.name), 'different bytes');
    assert.throws(publish, 'digest mismatch must reject a changed archive');
    assert.equal(JSON.parse(await readFile(statePath, 'utf8')).uploads, 4);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('Client metadata rejects the retired Probe bootstrap keyring field', async () => {
  await mkdir('build/release', { recursive: true });
  const artifact = 'build/release/cineko-client-v2.3.4-linux-amd64.tar.gz';
  await writeFile(artifact, 'client-fixture');
  const baseEnvironment = {
    ...process.env,
    CINEKO_MINIMUM_LAUNCHER_VERSION: '1.0.0',
    CINEKO_BROWSER_REVISION: '1228',
    CINEKO_PLAYWRIGHT_VERSION: '1.61.1',
    CINEKO_RELEASE_PUBLISHED_AT: '2026-08-12T09:00:00Z',
  };
  try {
    execFileSync('node', [
      'scripts/write-release-metadata.mjs', 'client', '2.3.4', 'linux/amd64', artifact, 'Cineko',
    ], { env: baseEnvironment });
    const metadataPath = 'build/release/client-release-linux-amd64.json';
    const metadata = JSON.parse(await readFile(metadataPath, 'utf8'));
    metadata.probeBootstrapPublicKeys = { primary: 'retired' };
    await writeFile(metadataPath, JSON.stringify(metadata));
    assert.throws(() => execFileSync('node', ['scripts/verify-release-metadata.mjs', metadataPath], { stdio: 'pipe' }));
  } finally {
    await rm(artifact, { force: true });
    await rm('build/release/client-release-linux-amd64.json', { force: true });
  }
});

test('Unix packagers emit executable independent artifacts', async () => {
  const root = await mkdtemp(join(tmpdir(), 'cineko-packaging-'));
  const client = join(root, 'Cineko');
  const driverNode = join(root, 'Library/Caches/ms-playwright-go/1.61.1/node');
  const driverCLI = join(root, 'Library/Caches/ms-playwright-go/1.61.1/package/cli.js');
  const generated = [
    'cineko-client-v2.3.4-darwin-arm64.tar.gz', 'client-release-darwin-arm64.json',
    'cineko-playwright-1.61.1-darwin-arm64.tar.gz', 'playwright-release-darwin-arm64.json',
  ].map((name) => `build/release/${name}`);
  const env = {
    ...process.env,
    HOME: root,
    GOMODCACHE: goModuleCache,
    CINEKO_VERSION: '2.3.4',
    CINEKO_MINIMUM_LAUNCHER_VERSION: '1.0.0',
    CINEKO_BROWSER_REVISION: '1228',
    CINEKO_PLAYWRIGHT_VERSION: '1.61.1',
    CINEKO_RELEASE_PUBLISHED_AT: '2026-08-12T09:00:00Z',
  };
  try {
    await Promise.all([
      mkdir(join(driverCLI, '..'), { recursive: true }),
    ]);
    await Promise.all([
      writeFile(client, 'client'), writeFile(driverNode, 'node'), writeFile(driverCLI, 'cli'),
    ]);
    await Promise.all([chmod(client, 0o755), chmod(driverNode, 0o755)]);
    execFileSync('bash', ['scripts/package-client.sh', 'darwin/arm64', client, 'Cineko'], { env });
    execFileSync('bash', ['scripts/package-playwright.sh', 'darwin/arm64'], { env });
    execFileSync('node', ['scripts/verify-release-metadata.mjs', generated[1], generated[3]]);
  } finally {
    await Promise.all(generated.map((path) => rm(path, { force: true })));
    await rm(root, { recursive: true, force: true });
  }
});

test('official browser publisher preserves verified bytes for GitHub hosting', async () => {
  const root = await mkdtemp(join(tmpdir(), 'cineko-official-browser-'));
  const tools = join(root, 'bin');
  const fixtures = join(root, 'fixtures');
  const registration = join(root, 'registration.json');
  const manifest = join(root, 'browser-release-set.json');
  const repeatedManifest = join(root, 'browser-release-set-repeated.json');
  const reformattedManifest = join(root, 'browser-release-set-reformatted.json');
  const changedManifest = join(root, 'browser-release-set-changed.json');
  const legacyManifest = join(root, 'legacy-browser-release-set.json');
  const targets = [
    ['mac-arm64', 'chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing'],
    ['linux64', 'chrome-linux64/chrome'],
    ['win64', 'chrome-win64/chrome.exe'],
  ];
  try {
    await Promise.all([mkdir(tools, { recursive: true }), mkdir(fixtures, { recursive: true })]);
    for (const [platform, executable] of targets) {
      const archiveRoot = join(root, platform);
      await mkdir(join(archiveRoot, executable, '..'), { recursive: true });
      await writeFile(join(archiveRoot, executable), platform);
      execFileSync('zip', ['-q', '-r', join(fixtures, `chrome-${platform}.zip`), executable.split('/')[0]], { cwd: archiveRoot });
    }
    const curl = join(tools, 'curl');
    await writeFile(curl, `#!/usr/bin/env bash
set -euo pipefail
url="\${@: -1}"
output=''
while [[ $# -gt 0 ]]; do
  case "$1" in
    --output) output="$2"; shift 2 ;;
    *) shift ;;
  esac
done
platform="\${url%/chrome-*.zip}"
platform="\${platform##*/}"
cp "$FAKE_BROWSER_FIXTURES/chrome-$platform.zip" "$output"
`);
    await chmod(curl, 0o755);
    const env = {
      ...process.env,
      PATH: `${tools}:${process.env.PATH}`,
      FAKE_BROWSER_FIXTURES: fixtures,
      CINEKO_RELEASE_PUBLISHED_AT: '2026-08-19T00:00:00Z',
      CINEKO_BROWSER_RELEASE_PAYLOAD_OUT: registration,
      CINEKO_BROWSER_ASSETS_DIR: join(root, 'archives'),
      GITHUB_REPOSITORY: 'cineko-org/client',
    };
    execFileSync('bash', ['scripts/publish-official-browser-release.sh', '1228', '149.0.7827.55', '1.61.1'], { env });
    const releaseSet = JSON.parse(await readFile(registration, 'utf8'));
    assert.deepEqual(releaseSet.releases.map(({ platform, architecture }) => `${platform}/${architecture}`), [
      'darwin/arm64', 'linux/amd64', 'windows/amd64',
    ]);
    assert.equal(releaseSet.releases.every(({ artifact }) => artifact.url.startsWith(
      'https://github.com/cineko-org/client/releases/download/chrome-v149.0.7827.55/',
    )), true);
    assert.equal(releaseSet.releases.every(({ artifact }) => /^[0-9a-f]{64}$/.test(artifact.sha256)), true);

    execFileSync('bash', ['scripts/publish-official-browser-release.sh', '1228', '149.0.7827.55', '1.61.1'], {
      env: {
        ...env,
        CINEKO_BROWSER_RELEASE_PAYLOAD_OUT: manifest,
        CINEKO_RELEASE_PUBLISH_TOKEN: '',
      },
    });
    assert.deepEqual(
      JSON.parse(await readFile(manifest, 'utf8')),
      releaseSet,
      'manifest-only mode must preserve the exact verified release set',
    );
    const fingerprint = execFileSync('go', [
      'run', '-mod=vendor', './cmd/releasecontract', 'fingerprint', 'browser', manifest,
    ], { env: { ...process.env, GOWORK: 'off' }, encoding: 'utf8' }).trim();
    assert.match(fingerprint, /^[0-9a-f]{64}$/, 'latest generated Proto must produce a semantic content address');

    execFileSync('bash', ['scripts/publish-official-browser-release.sh', '1228', '149.0.7827.55', '1.61.1'], {
      env: {
        ...env,
        CINEKO_BROWSER_RELEASE_PAYLOAD_OUT: repeatedManifest,
        CINEKO_RELEASE_PUBLISH_TOKEN: '',
      },
    });
    assert.deepEqual(
      JSON.parse(await readFile(repeatedManifest, 'utf8')),
      releaseSet,
      'repeated generation must preserve the exact latest-Proto release set',
    );
    assert.equal(execFileSync('go', [
      'run', '-mod=vendor', './cmd/releasecontract', 'fingerprint', 'browser', repeatedManifest,
    ], { env: { ...process.env, GOWORK: 'off' }, encoding: 'utf8' }).trim(), fingerprint, 'repeated generation must preserve the semantic tag');

    await writeFile(reformattedManifest, JSON.stringify(releaseSet));
    const browserTag = `browser-r1228-${fingerprint}`;
    execFileSync('bash', [
      'scripts/verify-browser-release-identity.sh', browserTag, manifest, reformattedManifest,
    ], { env: process.env });
    const changedReleaseSet = structuredClone(releaseSet);
    changedReleaseSet.releases[0].artifact.sha256 = '0'.repeat(64);
    await writeFile(changedManifest, JSON.stringify(changedReleaseSet));
    assert.throws(() => execFileSync('bash', [
      'scripts/verify-browser-release-identity.sh', browserTag, manifest, changedManifest,
    ], { env: process.env, stdio: 'pipe' }), 'semantic release changes must not reuse an immutable browser tag');

    await writeFile(legacyManifest, JSON.stringify({ schemaVersion: 1, ...releaseSet }));
    assert.throws(() => execFileSync('go', [
      'run', '-mod=vendor', './cmd/releasecontract', 'fingerprint', 'browser', legacyManifest,
    ], { env: { ...process.env, GOWORK: 'off' }, stdio: 'pipe' }), 'legacy schemaVersion metadata must be rejected');

    for (const args of [
      ['0', '149.0.7827.55', '1.61.1'],
      ['1228', '149.0.7827', '1.61.1'],
      ['1228', '149.0.7827.55', 'invalid'],
    ]) {
      assert.throws(() => execFileSync('bash', ['scripts/publish-official-browser-release.sh', ...args], { env, stdio: 'pipe' }));
    }
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
