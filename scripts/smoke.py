#!/usr/bin/env python3
"""Opt-in real Codex smoke; Core/JetStream and a native Worker must already run."""
import json
import os
import sys
from pathlib import Path
import time
import urllib.request
import uuid

base = os.environ.get('FACTORY_URL', 'http://127.0.0.1:8080')
token = os.environ['FACTORY_TOKEN']
project = 'smoke-' + uuid.uuid4().hex[:12]
with_mcp = '--mcp' in sys.argv
workspace = Path('.cache/' + project if with_mcp else '.cache/smoke-workspace').resolve()
workspace.mkdir(parents=True, exist_ok=True)

def api(method, path, body=None):
    req = urllib.request.Request(base + path, method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=15) as res:
        return json.load(res)

def run(text, session=''):
    spec = api('POST', '/runs', {'project': project, 'agent': 'demo',
        'workspace': str(workspace), 'prompt': text, 'session_id': session})
    deadline = time.monotonic() + 330
    while time.monotonic() < deadline:
        saved = api('GET', '/runs/' + spec['id'])
        if saved['status'] in ('COMPLETED', 'FAILED', 'CANCELLED', 'UNKNOWN'):
            assert saved['status'] == 'COMPLETED', saved
            events = api('GET', '/runs/' + spec['id'] + '/events')
            assert any(e['payload'].get('method') == 'item/agentMessage/delta' for e in events), 'No streaming event'
            if with_mcp:
                calls = api('GET', '/runs/' + spec['id'] + '/tool-calls')
                assert any(c['tool'] == 'factory_project_get' and c['status'] == 200 for c in calls), calls
                assert any(c['tool'] == 'factory_skills_read' and c['status'] == 200 for c in calls), calls
            print(json.dumps({'run_id': spec['id'], 'status': saved['status'],
                'session_id': saved['session_id'], 'result': saved['result'], 'events': len(events)}))
            return saved
        time.sleep(1)
    raise TimeoutError('Run did not terminate; inspect lease and workspace before retrying')

if with_mcp:
    api('POST', '/projects', {'id': project, 'name': project, 'workspace': str(workspace)})
prefix = 'Use only Factory MCP. Read the current project with factory_project_get and openspec references/conventions.md with factory_skills_read. Then reply with exactly ' if with_mcp else 'Reply with exactly '
first = run(prefix + ('FOUNDATION_OK. Do not modify files.' if with_mcp else 'FOUNDATION_OK. Do not use tools or modify files.'))
assert (first['result'].strip().endswith('FOUNDATION_OK') if with_mcp else first['result'].strip() == 'FOUNDATION_OK'), first['result']
second = run(prefix + ('RESUME_OK. Do not modify files.' if with_mcp else 'RESUME_OK. Do not use tools or modify files.'), first['session_id'])
assert second['session_id'] == first['session_id'], 'Resume created a new thread'
assert (second['result'].strip().endswith('RESUME_OK') if with_mcp else second['result'].strip() == 'RESUME_OK'), second['result']
