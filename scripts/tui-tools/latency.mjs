// Delay each direction by half the requested RTT, scheduling chunks by
// arrival time. Never sleep once per chunk: that would throttle throughput.
import net from 'node:net';
import {performance} from 'node:perf_hooks';
import {setTimeout as sleep} from 'node:timers/promises';
import {pipeline} from 'node:stream/promises';
import {Transform} from 'node:stream';

const [listen, target, rtt] = process.argv.slice(2).map(Number);
if (![listen, target].every(p => Number.isInteger(p) && p > 0 && p <= 65535) ||
    listen === target || !Number.isFinite(rtt) || rtt < 0) {
  throw new Error('Usage: node latency.mjs LISTEN_PORT GAME_PORT ADDED_RTT_MS');
}

async function relay(from, to) {
  const timestamp = new Transform({readableObjectMode: true, transform(data, _, done) {
    done(null, {data, due: performance.now() + rtt / 2});
  }});
  await pipeline(from, timestamp, async function* (chunks, {signal}) {
    for await (const {data, due} of chunks) {
      await sleep(Math.max(0, due - performance.now()), undefined, {signal});
      yield data;
    }
  }, to);
}

net.createServer({allowHalfOpen: true}, client => {
  const game = net.connect({host: '127.0.0.1', port: target, allowHalfOpen: true});
  client.setNoDelay(true);
  game.setNoDelay(true);
  Promise.all([relay(client, game), relay(game, client)]).catch(error => {
    console.error(error.message);
    client.destroy();
    game.destroy();
  });
}).listen(listen, '127.0.0.1', () => console.log(`127.0.0.1:${listen} → :${target}, +${rtt} ms RTT`));
