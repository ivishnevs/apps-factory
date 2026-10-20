#!/usr/bin/env python3
"""Opt-in real Claude + Codex on one task (runtime-drivers 4.2): a Claude developer implements and submits, a
Codex reviewer approves, the developer ends the task. Records both runtimes' sessions, exits and costs.

Needs a Core serving the smoke company (FACTORY_COMPANY=smoke, configs/smoke/: claude-demo on Claude Haiku,
demo on the cheapest Codex model) with FACTORY_EXPLICIT_EXITS=1, and a node with both drivers enabled
(docs/development.md, Live smokes). A project without a repository: no git facts, no pull request.
Three model runs; the first failed step (assert, FAILED task, timeout) stops the script."""
import json
import os
from pathlib import Path
import time
import urllib.request
import uuid

base = os.environ.get('FACTORY_URL', 'http://127.0.0.1:8080')
token = os.environ['FACTORY_TOKEN']
developer = os.environ.get('SMOKE_DEVELOPER', 'claude-demo')
reviewer = os.environ.get('SMOKE_REVIEWER', 'demo')
project = 'drivers-smoke-' + uuid.uuid4().hex[:8]
workspace = Path('.cache/' + project).resolve()


def api(method, path, body=None):
    req = urllib.request.Request(base + path, method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=15) as response:
        return json.load(response)


def preflight(exits):
    """Refuses to start (no model run) unless Core serves the smoke company with the expected exits switch."""
    health = api('GET', '/health')
    company = os.environ.get('SMOKE_COMPANY', 'smoke')
    assert company == 'any' or health.get('company') == company, f"Core serves company {health.get('company')!r}, not {company!r}: start it with FACTORY_COMPANY={company} (or SMOKE_COMPANY=any)"
    assert health.get('explicit_exits') == exits, f"Core explicit_exits={health.get('explicit_exits')!r}; this smoke needs FACTORY_EXPLICIT_EXITS={'1' if exits == 'on' else '0'}"


def wait(task_id, done, seconds=330):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        task = api('GET', '/tasks/' + task_id)
        if done(task):
            return task
        assert task['status'] not in ('FAILED', 'CANCELLED'), task
        time.sleep(1)
    raise TimeoutError('task ' + task_id + ': ' + json.dumps(api('GET', '/tasks/' + task_id)))


preflight('on')
workspace.mkdir(parents=True, exist_ok=True)
api('POST', '/projects', {'id': project, 'name': project, 'workspace': str(workspace)})
text = f'''Bounded two-runtime smoke; keep every step minimal and use no other agents.
Developer ({developer}): create greeting.txt in the working directory containing exactly DRIVERS_OK and one newline. Then end the run with factory_exit_submit, summary DRIVERS_SUBMIT_OK, checks ["wrote greeting.txt"], reviewer {reviewer}.
Reviewer ({reviewer}): check with one shell command that greeting.txt is exactly DRIVERS_OK plus a newline. If it is, end the run with factory_exit_handoff, to {developer}, verdict approved, note DRIVERS_REVIEW_OK; otherwise verdict changes_requested with the difference as feedback.
Developer after an approval: change nothing and end the run at once with factory_exit_done, summary DRIVERS_DONE_OK.'''
task = api('POST', '/tasks', {'project_id': project, 'title': 'Two runtimes, one task', 'description': text,
    'assignee': developer, 'ready': True, 'request_id': project + '-task'})
task = wait(task['id'], lambda t: t['status'] == 'IN_REVIEW' and t['assignee'] == reviewer)
assert task['submission']['summary'] == 'DRIVERS_SUBMIT_OK' and task['implementer'] == developer, task
assert (workspace / 'greeting.txt').read_bytes() == b'DRIVERS_OK\n'
task = wait(task['id'], lambda t: t['status'] == 'DONE')
assert task['result'] == 'DRIVERS_DONE_OK', task
wait_runs = time.monotonic() + 60
while any(r['status'] in ('QUEUED', 'CLAIMED', 'RUNNING', 'CANCELLING') for r in api('GET', '/tasks/' + task['id'] + '/attempts')):
    assert time.monotonic() < wait_runs, 'runs still live'
    time.sleep(1)

runs = []
for run in api('GET', '/tasks/' + task['id'] + '/attempts'):
    calls = api('GET', '/runs/' + run['spec']['id'] + '/tool-calls')
    exits = [c['tool'] for c in calls if c['tool'].startswith('factory_exit_') and c['status'] == 200]
    runs.append({'run': run['spec']['id'], 'agent': run['spec']['agent'], 'provider': run['spec']['provider'],
        'model': run['spec'].get('runtime_options', {}).get('model'), 'session_id': run['session_id'],
        'status': run['status'], 'outcome': run.get('outcome'), 'exits': exits})
assert [r['provider'] for r in runs] == ['claude', 'codex', 'claude'], runs
assert [r['exits'] for r in runs] == [['factory_exit_submit'], ['factory_exit_handoff'], ['factory_exit_done']], runs
assert all(r['outcome'] == 'succeeded' and r['session_id'] for r in runs), runs
costs = api('GET', '/tasks/' + task['id'] + '/costs')
assert costs, 'no cost records'
summary = {'project': project, 'task': task['id'], 'status': task['status'], 'runs': runs,
    'claude_session_resumed': runs[0]['session_id'] == runs[2]['session_id'], 'costs': costs}
Path('.cache/drivers-verification.json').write_text(json.dumps(summary, indent=2) + '\n')
print(json.dumps(summary, indent=2))
