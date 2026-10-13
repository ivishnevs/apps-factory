#!/usr/bin/env python3
"""Opt-in real Codex approval/denial, owner inbox, restart and thread continuation."""
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
project = 'approvals-smoke-' + uuid.uuid4().hex[:10]
workspace = Path('.cache/' + project).resolve()
workspace.mkdir(parents=True, exist_ok=True)

def api(method, path, body=None):
    req = urllib.request.Request(base + path, method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=15) as response:
        return json.load(response)

def wait(path, predicate):
    deadline = time.monotonic() + 330
    while time.monotonic() < deadline:
        value = api('GET', path)
        if predicate(value):
            return value
        if isinstance(value, dict):
            assert value.get('status') not in ('FAILED', 'CANCELLED', 'UNKNOWN'), value
        time.sleep(.5)
    raise TimeoutError('Inspect durable state before retrying: ' + path)

api('POST', '/projects', {'id': project, 'name': project, 'workspace': str(workspace)})
results = []
for outcome in ('approve', 'reject'):
    target = api('POST', '/tasks', {'project_id': project, 'title': 'before-' + outcome,
        'request_id': 'target-' + outcome})
    action = {'name': 'factory_task_update', 'arguments': {'id': target['id'],
        'version': target['version'], 'title': 'after-' + outcome, 'request_id': 'exact-' + outcome}}
    prompt = '''Use only Factory MCP, never change files. This is an approval workflow smoke test.
The exact action is ACTION_JSON.
If your current task prompt has no explicit owner approval decision, call factory_owner_request_approval with call equal to that exact action, reason "Approve this exact test title change", blocking true, request_id "ask-OUTCOME". Do NOT execute the action yet. Finish the turn immediately with WAITING_APPROVAL.
When resumed after an owner decision, read factory_approval_get using the approval ID in the explicit answer. If APPROVED, execute the stored exact call with identical original arguments; finish APPROVAL_EXECUTED_OK. If REJECTED, attempt that same exact call once to verify Core returns 423, then finish APPROVAL_REJECTED_OK. Never change parameters or request_id to bypass a decision. Do not submit or ask again.'''.replace('ACTION_JSON', json.dumps(action)).replace('OUTCOME', outcome)
    task = api('POST', '/tasks', {'project_id': project, 'title': 'Approval ' + outcome,
        'description': prompt, 'assignee': 'demo', 'request_id': 'requester-' + outcome})
    api('POST', '/tasks/' + task['id'] + '/actions', {'version': task['version'], 'action': 'ready'})
    task = wait('/tasks/' + task['id'], lambda t: t['status'] == 'BLOCKED')
    grants = api('GET', '/approvals?project=' + project)
    grant = next(a for a in grants if a['task_id'] == task['id'])
    assert grant['status'] == 'PENDING' and grant['call'] == action, grant
    inbox = api('GET', '/inbox?project=' + project)
    assert any(m.get('approval_id') == grant['id'] for m in inbox), inbox
    if outcome == 'approve' and '--restart-core' in sys.argv:
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
        assert api('GET', '/approvals/' + grant['id']) == grant
    decision = {'version': grant['version'], 'action': outcome, 'reason': 'Owner smoke decision ' + outcome, 'request_id': 'owner-' + outcome}
    resolved = api('POST', '/approvals/' + grant['id'] + '/decision', decision)
    assert api('POST', '/approvals/' + grant['id'] + '/decision', decision) == resolved
    task = wait('/tasks/' + task['id'], lambda t: t['status'] == 'IN_REVIEW')
    expected = 'APPROVAL_EXECUTED_OK' if outcome == 'approve' else 'APPROVAL_REJECTED_OK'
    assert expected in task['result'] and task['attempt'] == 2, task
    attempts = api('GET', '/tasks/' + task['id'] + '/attempts')
    assert attempts[0]['session_id'] == attempts[1]['session_id'], attempts
    actual = api('GET', '/tasks/' + target['id'])
    assert actual['title'] == ('after-approve' if outcome == 'approve' else 'before-reject'), actual
    calls = api('GET', '/runs/' + task['current_run_id'] + '/tool-calls')
    updates = [c for c in calls if c['tool'] == 'factory_task_update']
    assert updates and all(c['status'] == (200 if outcome == 'approve' else 423) for c in updates), calls
    if outcome == 'approve':
        assert all(c.get('approval_id') == grant['id'] for c in updates), updates
    task = api('POST', '/tasks/' + task['id'] + '/review', {'version': task['version'], 'accept': True})
    results.append({'outcome': outcome, 'approval': resolved, 'task': task['id'], 'attempts': attempts, 'calls': calls, 'target': actual})
assert not api('GET', '/inbox?project=' + project)
summary = {'project': project, 'results': results, 'inbox_empty': True}
Path('.cache/approvals-verification.json').write_text(json.dumps(summary, indent=2) + '\n')
print(json.dumps(summary, indent=2))
