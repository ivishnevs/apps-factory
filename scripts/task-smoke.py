#!/usr/bin/env python3
"""Opt-in real Codex task chain. Start Core/NATS and a native Worker first."""
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

base = os.environ.get('FACTORY_URL', 'http://127.0.0.1:8080')
token = os.environ['FACTORY_TOKEN']
project = 'task-smoke-' + uuid.uuid4().hex[:10]
workspace = Path('.cache/' + project).resolve()
workspace.mkdir(parents=True, exist_ok=True)

def api(method, path, body=None):
    if body is not None:
        body.setdefault('request_id', uuid.uuid4().hex)
    req = urllib.request.Request(base + path, method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=15) as response:
        return json.load(response)

def wait(task_id, status):
    deadline = time.monotonic() + 330
    while time.monotonic() < deadline:
        task = api('GET', '/tasks/' + task_id)
        if task['status'] == status:
            return task
        assert task['status'] not in ('FAILED', 'CANCELLED', 'BLOCKED'), task
        time.sleep(.5)
    raise TimeoutError('Task did not reach ' + status + '; inspect before retrying')

api('POST', '/projects', {'id': project, 'name': project, 'workspace': str(workspace)})
first = api('POST', '/tasks', {'project_id': project, 'title': 'First smoke task',
    'description': 'Reply with exactly TASK_ONE_OK. Do not use tools or modify files.', 'assignee': 'demo'})
second = api('POST', '/tasks', {'project_id': project, 'title': 'Dependent smoke task',
    'description': 'Reply with exactly TASK_TWO_OK. Do not use tools or modify files.',
    'assignee': 'demo', 'dependencies': [first['id']]})
for task in (first, second):
    api('POST', '/tasks/' + task['id'] + '/actions', {'version': task['version'], 'action': 'ready'})
first = wait(first['id'], 'IN_REVIEW')
assert 'TASK_ONE_OK' in first['result'], first
pending = api('GET', '/tasks/' + second['id'])
assert pending['status'] == 'READY' and pending['attempt'] == 0, pending
if '--restart-core' in sys.argv:
    subprocess.run(['docker', 'compose', 'restart', 'core'], check=True)
    deadline = time.monotonic() + 30
    while True:
        try:
            api('GET', '/health')
            break
        except (urllib.error.URLError, ConnectionError):
            if time.monotonic() > deadline:
                raise
            time.sleep(.5)
    assert api('GET', '/tasks/' + first['id'])['status'] == 'IN_REVIEW'
first = api('POST', '/tasks/' + first['id'] + '/review', {'version': first['version'], 'accept': True})
second = wait(second['id'], 'IN_REVIEW')
assert 'TASK_TWO_OK' in second['result'], second
second = api('POST', '/tasks/' + second['id'] + '/review', {'version': second['version'], 'accept': True})
attempts = [api('GET', '/tasks/' + t['id'] + '/attempts') for t in (first, second)]
assert all(len(a) == 1 and a[0]['spec']['config_hash'] for a in attempts), attempts
assert attempts[0][0]['session_id'] != attempts[1][0]['session_id'], 'Tasks shared a Codex thread'
print(json.dumps({'project': project, 'restart_tested': '--restart-core' in sys.argv,
    'tasks': [{'id': t['id'], 'status': t['status'], 'attempt': t['attempt'], 'result': t['result']} for t in (first, second)]}, indent=2))
