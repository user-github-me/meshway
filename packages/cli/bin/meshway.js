#!/usr/bin/env node
import { spawn } from 'node:child_process';
import { installCore, resolveCore } from '../src/installer.js';
import { readFile } from 'node:fs/promises';

const args = process.argv.slice(2);
try {
  if (args[0] === '--help' || args[0] === 'help') {
    console.log('Meshway: encrypted peer VPN, SOCKS and tunnels.\n\nFirst install: meshway install-core\nStart/setup: meshway init | setup-server --personal | daemon | account | status | version\n\nInstaller options: --release-base HTTPS_URL --public-key FORK_PUBLIC_KEY_FILE --version VERSION\nThe installer verifies the publisher signature and updates only when you request it.');
    process.exit(0);
  }
  if (args[0] === 'install-core') {
    const options = {};
    for (let i = 1; i < args.length; i += 2) {
      if (!['--release-base', '--public-key', '--version'].includes(args[i]) || !args[i + 1] || options[args[i]]) throw new Error('Usage: meshway install-core [--release-base HTTPS_URL] [--public-key FORK_PUBLIC_KEY_FILE] [--version VERSION]');
      options[args[i]] = args[i + 1];
    }
    console.log(`Installed verified core: ${await installCore({ base: options['--release-base'], publisherKey: options['--public-key'] ? await readFile(options['--public-key'], 'utf8') : undefined, expectedVersion: options['--version'] })}`);
  } else {
    const executable = await resolveCore();
    const child = spawn(executable, args, { stdio: 'inherit', shell: false });
    for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => child.kill(signal));
    child.on('error', error => { console.error(`meshway: ${error.message}`); process.exitCode = 1; });
    child.on('exit', (code, signal) => { process.exitCode = code ?? (signal ? 1 : 0); });
  }
} catch (error) { console.error(`meshway: ${error.message}`); process.exitCode = 1; }
