import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, extname, join, relative, resolve, sep } from 'node:path';

const sourceRoot = resolve('src');

function filesUnder(path) {
  return readdirSync(path).flatMap((name) => {
    const value = join(path, name);
    return statSync(value).isDirectory() ? filesUnder(value) : [value];
  });
}

const files = filesUnder(sourceRoot);
const violations = [];

for (const file of files) {
  const name = relative(sourceRoot, file);
  if (extname(file) === '.css') violations.push(`${name}: authored CSS is not allowed; use Mantine theme and style props`);
  if (!/\.[cm]?[jt]sx?$/.test(file)) continue;
  const source = readFileSync(file, 'utf8');
  const imports = [...source.matchAll(/(?:import|export)\s+(?:[^'";]+?\s+from\s+)?['"]([^'"]+)['"]/g)].map((match) => match[1]);

  if (name.startsWith(`components${sep}core${sep}`)) {
    const coreRoot = resolve(sourceRoot, 'components/core');
    for (const imported of imports) {
      if (!imported.startsWith('.')) continue;
      const target = resolve(dirname(file), imported);
      if (relative(coreRoot, target).startsWith(`..${sep}`)) {
        violations.push(`${name}: Core UI import escapes its boundary: ${imported}`);
      }
    }
  }

	if (name.startsWith(`features${sep}`)) {
		const owningFeature = name.split(sep)[1];
		for (const imported of imports) {
			if (!imported.startsWith('.')) continue;
			const target = relative(sourceRoot, resolve(dirname(file), imported)).split(sep);
			const targetFeatureIndex = target.indexOf('features');
			const targetFeature = targetFeatureIndex >= 0 ? target[targetFeatureIndex + 1] : '';
			if (targetFeature && targetFeature !== owningFeature) {
				violations.push(`${name}: Feature imports another feature implementation: ${imported}`);
			}
		}
	}

  if (name.includes(`${sep}ui${sep}`)) {
    const parts = name.split(sep);
    const featureIndex = parts.indexOf('features');
    const owningFeature = featureIndex >= 0 ? parts[featureIndex + 1] : '';
    for (const imported of imports) {
      if (imported.includes('/api/client') || imported.includes('/use') || imported.includes('/app/')) {
        violations.push(`${name}: Feature UI imports orchestration dependency: ${imported}`);
      }
      if (owningFeature && imported.startsWith('.')) {
        const target = relative(sourceRoot, resolve(dirname(file), imported)).split(sep);
        const targetFeatureIndex = target.indexOf('features');
        const targetFeature = targetFeatureIndex >= 0 ? target[targetFeatureIndex + 1] : '';
        if (targetFeature && targetFeature !== owningFeature) {
          violations.push(`${name}: Feature UI imports another feature: ${imported}`);
        }
      }
    }
    if (/\b(fetch|desktopBridge)\s*\(/.test(source) || /window\.(?:go|runtime)/.test(source)) {
      violations.push(`${name}: Feature UI performs an external side effect`);
    }
  }

  if (/<(?:div|section|article|header|main|nav|button|input|select|label|form|strong|span|p|h[1-6]|br)(?:\s|\/|>)/.test(source)) {
    violations.push(`${name}: raw HTML JSX is not allowed; use Mantine components`);
  }
}

if (violations.length > 0) {
  process.stderr.write(`${violations.join('\n')}\n`);
  process.exitCode = 1;
}
