import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { chmod, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';

process.env.CINEKO_RELEASES_PUBLIC_BASE_URL = 'https://releases.example.com/cineko';
const goModuleCache = execFileSync('go', ['env', 'GOMODCACHE'], { encoding: 'utf8' }).trim();

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
          CINEKO_PROBE_BOOTSTRAP_PUBLIC_KEYS_JSON: '{"2026-08":"public-key"}',
          CINEKO_RELEASE_PUBLISHED_AT: '2026-08-12T09:00:00Z',
        },
      });
    }
    execFileSync('node', ['scripts/verify-release-metadata.mjs', ...outputs]);
    const client = JSON.parse(await readFile(outputs[0], 'utf8'));
    assert.equal(client.artifact.url, 'https://releases.example.com/cineko/client/v2.3.4/linux-amd64/cineko-client-v2.3.4-linux-amd64.tar.gz');
    assert.equal(client.playwrightVersion, '1.61.1');
    assert.equal(client.architecture, 'amd64');
  } finally {
    await Promise.all([...fixtures.map(([, , filename]) => rm(`build/release/${filename}`, { force: true })), ...outputs.map((path) => rm(path, { force: true }))]);
  }
});

test('Client metadata rejects a missing or malformed Probe keyring', async () => {
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
    for (const keyring of ['', '{', '{}', '{"primary":""}']) {
      assert.throws(() => execFileSync('node', [
        'scripts/write-release-metadata.mjs', 'client', '2.3.4', 'linux/amd64', artifact, 'Cineko',
      ], { env: { ...baseEnvironment, CINEKO_PROBE_BOOTSTRAP_PUBLIC_KEYS_JSON: keyring }, stdio: 'pipe' }));
    }
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
    CINEKO_PROBE_BOOTSTRAP_PUBLIC_KEYS_JSON: '{"2026-08":"public-key"}',
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

test('official browser publisher produces verified Chrome for Testing metadata without rehosting', async () => {
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
    };
    execFileSync('bash', ['scripts/publish-official-browser-release.sh', '1228', '149.0.7827.55', '1.61.1'], { env });
    const releaseSet = JSON.parse(await readFile(registration, 'utf8'));
    assert.deepEqual(releaseSet.releases.map(({ platform, architecture }) => `${platform}/${architecture}`), [
      'darwin/arm64', 'linux/amd64', 'windows/amd64',
    ]);
    assert.equal(releaseSet.releases.every(({ artifact }) => artifact.url.startsWith(
      'https://storage.googleapis.com/chrome-for-testing-public/149.0.7827.55/',
    )), true);
    assert.equal(releaseSet.releases.every(({ artifact }) => /^[0-9a-f]{64}$/.test(artifact.sha256)), true);

    execFileSync('bash', ['scripts/publish-official-browser-release.sh', '1228', '149.0.7827.55', '1.61.1'], {
      env: {
        ...env,
        CINEKO_BROWSER_RELEASE_PAYLOAD_OUT: manifest,
        CINEKO_CENTRAL_URL: '',
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
        CINEKO_CENTRAL_URL: '',
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

test('publisher verifies S3 checksums without downloading release artifacts', async () => {
  const root = await mkdtemp(join(tmpdir(), 'cineko-publisher-'));
  const tools = join(root, 'bin');
  const objects = join(root, 'objects');
  const registration = join(root, 'registration');
  const platforms = [['darwin', 'arm64'], ['windows', 'amd64'], ['linux', 'amd64']];
  const artifacts = platforms.map(([platform, arch]) => `build/release/cineko-client-v2.3.4-${platform}-${arch}.tar.gz`);
  const metadata = platforms.map(([platform, arch]) => `build/release/client-release-${platform}-${arch}.json`);
  try {
    await Promise.all([mkdir(tools, { recursive: true }), mkdir(objects, { recursive: true })]);
    for (let index = 0; index < platforms.length; index += 1) {
      const [platform, arch] = platforms[index];
      await writeFile(artifacts[index], `immutable-client-${platform}-${arch}`);
      execFileSync('node', ['scripts/write-release-metadata.mjs', 'client', '2.3.4', `${platform}/${arch}`, artifacts[index], 'Cineko'], {
        env: {
          ...process.env,
          CINEKO_MINIMUM_LAUNCHER_VERSION: '1.0.0', CINEKO_BROWSER_REVISION: '1228',
          CINEKO_PLAYWRIGHT_VERSION: '1.61.1', CINEKO_PROBE_BOOTSTRAP_PUBLIC_KEYS_JSON: '{"2026-08":"public-key"}',
          CINEKO_RELEASE_PUBLISHED_AT: '2026-08-12T09:00:00Z',
        },
      });
    }
    const aws = join(tools, 'aws');
    await writeFile(aws, `#!/usr/bin/env bash
set -euo pipefail
if [[ " $* " == *" s3api head-object "* ]]; then
  object="$FAKE_OBJECTS/$(printf '%s' "$*" | sed -E 's/.* --key ([^ ]+).*/\\1/' | tr '/' '_')"
  test -f "$object"
  checksum="$(openssl dgst -sha256 -binary "$object" | openssl base64 -A)"
  checksum_hex="$(sha256sum "$object" | awk '{print $1}')"
  printf '{"ContentLength":%s,"ChecksumSHA256":"%s","Metadata":{"sha256":"%s"}}\\n' \
    "$(wc -c < "$object" | tr -d ' ')" "$checksum" "$checksum_hex"
  exit 0
fi
if [[ " $* " == *" s3api put-object "* ]]; then
  object="$FAKE_OBJECTS/$(printf '%s' "$*" | sed -E 's/.* --key ([^ ]+).*/\\1/' | tr '/' '_')"
  body="$(printf '%s' "$*" | sed -E 's/.* --body ([^ ]+).*/\\1/')"
  if [[ -f "$object" ]]; then exit 1; fi
  if [[ -n "\${FAKE_PUT_RACE_MARKER:-}" && ! -f "$FAKE_PUT_RACE_MARKER" ]]; then
    cp "$body" "$object"
    touch "$FAKE_PUT_RACE_MARKER"
    exit 1
  fi
  cp "$body" "$object"
  exit 0
fi
exit 2
`);
    await chmod(aws, 0o755);
    const env = {
      ...process.env, PATH: `${tools}:${process.env.PATH}`, FAKE_OBJECTS: objects,
      FAKE_PUT_RACE_MARKER: join(root, 'put-race'),
      CINEKO_RELEASES_S3_ENDPOINT: 'https://minio.example.test', CINEKO_RELEASES_S3_ACCESS_KEY: 'access',
      CINEKO_RELEASES_S3_SECRET_KEY: 'secret', CINEKO_RELEASE_PAYLOAD_OUT: registration,
    };
    execFileSync('bash', ['scripts/publish-release.sh', ...metadata], { env });
    const releaseSet = JSON.parse(await readFile(registration, 'utf8'));
    assert.equal(releaseSet.releases.length, 3);
    assert.equal(releaseSet.releases.every(({ architecture }) => ['arm64', 'amd64'].includes(architecture)), true);
    assert.deepEqual(releaseSet.releases.map(({ platform, architecture }) => `${platform}/${architecture}`).sort(), ['darwin/arm64', 'linux/amd64', 'windows/amd64']);

    const firstMetadata = await Promise.all(metadata.map((path) => readFile(path, 'utf8')));
    for (let index = 0; index < platforms.length; index += 1) {
      const [platform, arch] = platforms[index];
      execFileSync('node', ['scripts/write-release-metadata.mjs', 'client', '2.3.4', `${platform}/${arch}`, artifacts[index], 'Cineko'], {
        env: {
          ...process.env,
          CINEKO_MINIMUM_LAUNCHER_VERSION: '1.0.0', CINEKO_BROWSER_REVISION: '1228',
          CINEKO_PLAYWRIGHT_VERSION: '1.61.1', CINEKO_PROBE_BOOTSTRAP_PUBLIC_KEYS_JSON: '{"2026-08":"public-key"}',
          CINEKO_RELEASE_PUBLISHED_AT: '2026-08-12T09:00:00Z',
        },
      });
    }
    assert.deepEqual(await Promise.all(metadata.map((path) => readFile(path, 'utf8'))), firstMetadata);
    execFileSync('bash', ['scripts/publish-release.sh', ...metadata], { env });
    assert.deepEqual(JSON.parse(await readFile(registration, 'utf8')), releaseSet);

    const mismatchedTimestamp = JSON.parse(firstMetadata[2]);
    mismatchedTimestamp.publishedAt = '2026-08-12T09:00:01.000Z';
    await writeFile(metadata[2], `${JSON.stringify(mismatchedTimestamp, null, 2)}\n`);
    assert.throws(() => execFileSync('bash', ['scripts/publish-release.sh', ...metadata], { env, stdio: 'pipe' }));
    await writeFile(metadata[2], firstMetadata[2]);

    const corruptedObject = join(objects, 'client_v2.3.4_linux-amd64_cineko-client-v2.3.4-linux-amd64.tar.gz');
    await writeFile(corruptedObject, 'different-content');
    assert.throws(() => execFileSync('bash', ['scripts/publish-release.sh', ...metadata], { env, stdio: 'pipe' }));
    assert.throws(() => execFileSync('bash', ['scripts/publish-release.sh', ...metadata.slice(0, 2)], { env, stdio: 'pipe' }));
  } finally {
    await Promise.all([...artifacts.map((path) => rm(path, { force: true })), ...metadata.map((path) => rm(path, { force: true })), rm(root, { recursive: true, force: true })]);
  }
});
