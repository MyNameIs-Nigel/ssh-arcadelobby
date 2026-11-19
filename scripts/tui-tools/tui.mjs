// Record a development SSH PTY, or render its asciicast v2 output in xterm.js.
import {readFile, writeFile, mkdtemp, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join, extname} from 'node:path';
import {createRequire} from 'node:module';
import {execFileSync} from 'node:child_process';
import {once} from 'node:events';
import {setTimeout as sleep} from 'node:timers/promises';
import {chromium} from 'playwright-core';
import ssh2 from 'ssh2';

const require = createRequire(import.meta.url);
const [mode, ...args] = process.argv.slice(2);
const help = `node tui.mjs record PORT DEV_KEY SCENE.json OUTPUT.cast
node tui.mjs render INPUT.cast OUTPUT.png|OUTPUT.gif [FPS]`;

async function terminal(cols, rows, onInput = () => {}) {
  const browser = await chromium.launch({executablePath: process.env.CHROMIUM});
  try {
    const page = await browser.newPage({viewport: {width: 3200, height: 1800}});
    await page.setContent('<style>body{margin:0;background:#000}#term{display:inline-block}</style><div id="term"></div>');
    await page.addScriptTag({path: require.resolve('@xterm/xterm')});
    await page.addStyleTag({path: require.resolve('@xterm/xterm/css/xterm.css')});
    await page.exposeFunction('sendInput', onInput);
    await page.evaluate(([cols, rows]) => {
      window.term = new Terminal({cols, rows, fontFamily: 'DejaVu Sans Mono, monospace',
        fontSize: 14, cursorBlink: false, scrollback: 0, allowProposedApi: true});
      term.open(document.querySelector('#term'));
      term.onData(data => window.sendInput(data));
    }, [cols, rows]);
    return {
      write: text => page.evaluate(text => new Promise(done => term.write(text, done)), text),
      text: () => page.evaluate(() => Array.from({length: term.rows}, (_, i) =>
        term.buffer.active.getLine(i + term.buffer.active.viewportY)?.translateToString(true)).join('\n')),
      screenshot: path => page.locator('.xterm-screen').screenshot({path}),
      close: () => browser.close(),
    };
  } catch (error) {
    await browser.close();
    throw error;
  }
}

async function record(port, key, scenePath, path) {
  const scene = JSON.parse(await readFile(scenePath, 'utf8'));
  const ssh = new ssh2.Client();
  const privateKey = await readFile(key);
  let channel;
  const screen = await terminal(scene.cols, scene.rows, data => channel?.write(data));
  const events = [];
  let writes = Promise.resolve();
  let pending = '';
  let failure;
  function paint(text) {
    const scheduled = pending.length > 0;
    pending += text;
    if (scheduled || !pending) return;
    writes = writes.then(async () => {
      const batch = pending;
      pending = '';
      await screen.write(batch);
    }).catch(error => { failure = error; });
  }
  try {
    const ready = once(ssh, 'ready');
    ssh.connect({host: '127.0.0.1', port: Number(port), username: scene.user || 'capture', privateKey});
    await ready;
    channel = await new Promise((resolve, reject) =>
      ssh.shell({term: 'xterm-256color', cols: scene.cols, rows: scene.rows}, (err, ch) =>
        err ? reject(err) : resolve(ch)));
    const start = performance.now();
    const elapsed = () => (performance.now() - start) / 1000;
    const decoder = new TextDecoder();
    let bytes = 0;
    let closed = false;
    channel.on('error', () => { closed = true; });
    ssh.on('error', () => { closed = true; });
    channel.on('close', () => { closed = true; });
    channel.on('data', chunk => {
      bytes += chunk.length;
      const text = decoder.decode(chunk, {stream: true});
      events.push([elapsed(), 'o', text]);
      paint(text);
    });
    const measurements = [];
    for (const step of scene.steps) {
      const before = bytes;
      await sleep(step.wait_ms || 0);
      const started = performance.now();
      if (step.when) {
        await writes;
        if (!new RegExp(step.when, 'mu').test(await screen.text())) continue;
      }
      if (step.send) channel.write(step.send);
      if (step.expect || step.gone) {
        const target = new RegExp(step.expect || step.gone, 'mu');
        for (;;) {
          await writes;
          if (failure) throw failure;
          const text = await screen.text();
          const match = target.exec(text);
          if (step.gone ? !match : match) {
            const result = step.gone ? {disappeared: step.gone} : {matched: match[0]};
            measurements.push({...result, elapsed_ms: Math.round(performance.now() - started),
              bytes_received: bytes - before});
            break;
          }
          if (closed || performance.now() - started >= (step.timeout_ms || 10000))
            throw new Error(`Timed out or disconnected waiting for ${JSON.stringify(step)}. Last screen:\n${text}`);
          await sleep(10);
        }
      } else {
        measurements.push({wait_ms: step.wait_ms || 0, bytes_received: bytes - before});
      }
    }
    channel.pause();
    const duration = elapsed();
    await writes;
    if (failure) throw failure;
    if (closed) throw new Error('SSH session closed during capture');
    const header = {version: 2, width: scene.cols, height: scene.rows, duration};
    await writeFile(path, [header, ...events].map(x => JSON.stringify(x)).join('\n') + '\n');
    console.log(JSON.stringify({bytes, seconds: header.duration, steps: measurements}));
  } finally {
    channel?.pause();
    channel?.destroy();
    ssh.destroy();
    try { await writes; } finally { await screen.close(); }
  }
}

async function render(cast, path, fps = 8) {
  const [header, ...events] = (await readFile(cast, 'utf8')).trimEnd().split('\n').map(JSON.parse);
  const gif = extname(path).toLowerCase() === '.gif';
  if (!gif && extname(path).toLowerCase() !== '.png') throw new Error(help);
  if (header.version !== 2 || !(fps >= 1 && fps <= 30)) throw new Error('Expected asciicast v2 and 1–30 fps');
  const screen = await terminal(header.width, header.height);
  const dir = await mkdtemp(join(tmpdir(), 'arcade-frames-'));
  try {
    let cursor = 0;
    async function draw(until, image) {
      let text = '';
      while (cursor < events.length && events[cursor][0] <= until) {
        const [, type, data] = events[cursor++];
        if (type === 'o') text += data;
      }
      await screen.write(text);
      await screen.screenshot(image);
    }
    if (!gif) return await draw(Infinity, path);
    const duration = Math.max(header.duration || 0, events.at(-1)?.[0] || 0);
    for (let i = 0; i <= Math.ceil(duration * fps); i++) {
      await draw(i / fps, join(dir, String(i).padStart(6, '0') + '.png'));
    }
    execFileSync('ffmpeg', ['-loglevel', 'error', '-y', '-framerate', String(fps),
      '-i', join(dir, '%06d.png'), '-filter_complex_threads', '1', '-filter_complex',
      'split[a][b];[a]palettegen[p];[b][p]paletteuse', '-loop', '0', path]);
  } finally {
    await screen.close();
    await rm(dir, {recursive: true, force: true});
  }
}

try {
  if (mode === 'record' && args.length === 4) await record(...args);
  else if (mode === 'render' && [2, 3].includes(args.length)) await render(...args);
  else throw new Error(help);
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
