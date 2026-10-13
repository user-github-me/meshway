#!/usr/bin/env node
import { spawn } from 'node:child_process';
import { installCore, resolveCore } from '../src/installer.js';

const args = process.argv.slice(2);
try {
  if (args[0] === 'install-core') {
    if (args.length !== 3 || args[1] !== '--release-base') throw new Error('Usage: meshway install-core --release-base https://YOUR-RELEASE-DIRECTORY/');
    console.log(`Installed verified core: ${await installCore({ base: args[2] })}`);
  } else {
    const executable = await resolveCore();
    const child = spawn(executable, args, { stdio: 'inherit', shell: false });
    for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => child.kill(signal));
    child.on('error', error => { console.error(`meshway: ${error.message}`); process.exitCode = 1; });
    child.on('exit', (code, signal) => { process.exitCode = code ?? (signal ? 1 : 0); });
  }
} catch (error) { console.error(`meshway: ${error.message}`); process.exitCode = 1; }
