"""Exercise the real deployment script with a stateful Docker substitute."""
import base64
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('remote-deploy.sh').resolve()
DIGEST = 'sha256:' + 'a' * 64
OLD = 'sha256:' + 'b' * 64
DOCKER = r'''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
p=Path(os.environ['MOCK_STATE']); state=json.loads(p.read_text()); args=sys.argv[1:]
state['calls'].append(args)
p.write_text(json.dumps(state))
service=os.environ['SERVICE']; key=service.upper()+'_IMAGE'
def image():
    if state.get('legacy'): return 'legacy:latest'
    text = Path('.env').read_text() if Path('.env').exists() else ''
    fallback = os.environ['IMAGE'].split('@')[0]+':latest' if state.get('default_cached') else 'legacy:latest'
    return dict(line.split('=',1) for line in text.splitlines() if '=' in line).get(key,fallback)
if args[:4]==['compose','ps','--status','running']:
    if not state.get('down'): print('container')
elif args[:3]==['image','inspect','--format']:
    if state.get('first') or state.get('missing_cache'): sys.exit(1)
    print('sha256:'+'c'*64)
elif args[:2]==['inspect','--format']:
    print(state['image'] if args[2]=='{{.Config.Image}}' else 'sha256:'+'c'*64)
elif args[:3]==['compose','config','--images']: print(image())
elif args[:3]==['compose','exec','-T']:
    if args[3]=='web': sys.exit(1 if state.get('caddy_fail') and state['version']=='2.4.3' else 0)
    if not state.get('stopped') and not (state.get('rollback_bad') and state['image']!=os.environ['IMAGE']):
        print('SSH-2.0-'+state['version'])
elif args[:2]==['compose','up']:
    if args[-1]==service:
        new=image(); state['image']=new
        state['version']='wrong' if state.get('bad') and new==os.environ['IMAGE'] else ('2.4.3' if new==os.environ['IMAGE'] else '1.2.3')
        p.write_text(json.dumps(state))
elif args[:2]==['compose','stop']:
    state['stopped']=True; p.write_text(json.dumps(state))
elif args[:2]==['pull','--quiet']:
    sys.exit(1 if state.get('pull_fail') else 0)
elif args[0]=='tag': pass
else: raise SystemExit('unexpected docker call: '+str(args))
'''

class DeployTest(unittest.TestCase):
    def run_deploy(self, service='farm', **flags):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        path = Path(self.temp.name)
        binpath = path/'bin'; binpath.mkdir()
        (binpath/'docker').write_text(DOCKER)
        (binpath/'flock').write_text('#!/bin/sh\nexit 0\n')
        (binpath/'sleep').write_text('#!/bin/sh\nexit 0\n')
        for tool in binpath.iterdir(): tool.chmod(0o755)
        repo = 'ssh-arcadelobby' if service == 'router' else 'ssh-'+service
        self.old_image = f'ghcr.io/mynameis-nigel/{repo}@{OLD}'
        self.new_image = f'ghcr.io/mynameis-nigel/{repo}@{DIGEST}'
        state = {'image': self.old_image, 'version': '1.2.3', 'calls': [], **flags}
        (path/'state.json').write_text(json.dumps(state))
        if not flags.get('first') and not flags.get('default_cached'):
            (path/'.env').write_text(f'UNRELATED=keep\nCHESS_IMAGE=pinned-chess\n{service.upper()}_IMAGE={self.old_image}\n')
        for file in ['docker-compose.yml','games.toml','Caddyfile']: (path/file).write_text('old '+file)
        self.inode = (path/'games.toml').stat().st_ino
        env = {**os.environ, 'PATH': str(binpath)+os.pathsep+os.environ['PATH'],
               'MOCK_STATE': str(path/'state.json'), 'COMPOSE_DIR': str(path),
               'SERVICE': service, 'IMAGE': self.new_image, 'VERSION': '2.4.3', 'PROBE_ATTEMPTS': '1'}
        for variable, file in [('COMPOSE_B64','docker-compose.yml'),('GAMES_B64','games.toml'),('CADDY_B64','Caddyfile')]:
            env[variable] = base64.b64encode(('new '+file).encode()).decode()
        result = subprocess.run(['sh',str(SCRIPT)], env=env, text=True, capture_output=True)
        self.path = path
        self.state = json.loads((path/'state.json').read_text())
        self.result = result
        return result

    def test_success_pins_image_and_preserves_other_services(self):
        result = self.run_deploy()
        self.assertEqual(result.returncode, 0, result.stdout+result.stderr)
        env = (self.path/'.env').read_text()
        self.assertIn('FARM_IMAGE='+self.new_image, env)
        self.assertIn('CHESS_IMAGE=pinned-chess', env)
        self.assertIn('UNRELATED=keep', env)
        starts = [call for call in self.state['calls'] if call[:2] == ['compose','up']]
        self.assertEqual(starts, [['compose','up','-d','--no-deps','--pull','never','farm']])

    def test_wrong_version_rolls_back_and_reports_failure(self):
        self.assertNotEqual(self.run_deploy(bad=True).returncode, 0)
        self.assertEqual(self.state['image'], self.old_image)
        self.assertIn('FARM_IMAGE='+self.old_image, (self.path/'.env').read_text())
        self.assertIn('Previous build restored', self.result.stdout)

    def test_service_down_restores_configured_image(self):
        self.assertNotEqual(self.run_deploy(down=True, bad=True).returncode, 0)
        self.assertEqual(self.state['image'], self.old_image)
        self.assertIn('FARM_IMAGE='+self.old_image, (self.path/'.env').read_text())
        self.assertIn('Previous build restored', self.result.stdout)

    def test_down_service_without_cached_image_stops_failed_container(self):
        self.assertNotEqual(self.run_deploy(down=True, bad=True, missing_cache=True).returncode, 0)
        self.assertTrue(self.state.get('stopped'))
        self.assertIn('FARM_IMAGE='+self.old_image, (self.path/'.env').read_text())
        self.assertNotIn('Previous build restored', self.result.stdout)

    def test_down_service_snapshots_cached_default_before_first_pin(self):
        self.assertNotEqual(self.run_deploy(down=True, default_cached=True, bad=True).returncode, 0)
        self.assertTrue(self.state['image'].endswith(':rollback'))
        self.assertIn('Previous build restored', self.result.stdout)
        tags = [call for call in self.state['calls'] if call[0] == 'tag']
        self.assertEqual(len(tags), 1)
        self.assertEqual(tags[0][1], 'sha256:'+'c'*64)

    def test_first_deployment_failure_stops_new_container(self):
        self.assertNotEqual(self.run_deploy(down=True, first=True, bad=True).returncode, 0)
        self.assertTrue(self.state.get('stopped'), self.result.stdout+self.result.stderr)
        self.assertFalse((self.path/'.env').exists())
        self.assertNotIn('Previous build restored', self.result.stdout)

    def test_down_service_rollback_requires_healthy_banner(self):
        self.assertNotEqual(self.run_deploy(down=True, bad=True, rollback_bad=True).returncode, 0)
        self.assertIn('ROLLBACK FAILED', self.result.stderr)
        self.assertNotIn('Previous build restored', self.result.stdout)

    def test_pull_failure_leaves_running_service_untouched(self):
        self.assertNotEqual(self.run_deploy(pull_fail=True).returncode, 0)
        self.assertEqual(self.state['image'], self.old_image)
        self.assertFalse(any(call[:2]==['compose','up'] for call in self.state['calls']))

    def test_legacy_compose_fails_before_restart(self):
        self.assertNotEqual(self.run_deploy(legacy=True).returncode, 0)
        self.assertFalse(any(call[:2]==['compose','up'] for call in self.state['calls']))
        self.assertIn('FARM_IMAGE='+self.old_image, (self.path/'.env').read_text())

    def test_router_sync_preserves_bind_mount_inode(self):
        result = self.run_deploy('router')
        self.assertEqual(result.returncode, 0, result.stdout+result.stderr)
        self.assertEqual((self.path/'games.toml').read_text(), 'new games.toml')
        self.assertEqual((self.path/'games.toml').stat().st_ino, self.inode)
        self.assertIn('CHESS_IMAGE=pinned-chess', (self.path/'.env').read_text())

    def test_router_caddy_failure_restores_all_configuration(self):
        self.assertNotEqual(self.run_deploy('router', caddy_fail=True).returncode, 0)
        self.assertEqual(self.state['image'], self.old_image)
        for file in ['docker-compose.yml','games.toml','Caddyfile']:
            self.assertEqual((self.path/file).read_text(), 'old '+file)
        self.assertEqual((self.path/'games.toml').stat().st_ino, self.inode)

if __name__ == '__main__': unittest.main()
