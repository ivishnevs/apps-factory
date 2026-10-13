#!/usr/bin/env python3
"""Opt-in real Codex: scoped autonomy and durable owner-rejection revision.
Run with .env exported and native Worker running. Use --resume CHECKPOINT after interruption.
"""
import json
import os
from pathlib import Path
import sys
import time
import urllib.request
import uuid

base = os.environ.get('FACTORY_URL', 'http://127.0.0.1:8080')
token = os.environ['FACTORY_TOKEN']
if len(sys.argv) == 3 and sys.argv[1] == '--resume':
    checkpoint = Path(sys.argv[2])
    state = json.loads(checkpoint.read_text())
else:
    project = 'autonomy-smoke-' + uuid.uuid4().hex[:10]
    workspace = Path('.cache/' + project).resolve()
    workspace.mkdir(parents=True, exist_ok=True)
    checkpoint = workspace / 'checkpoint.json'
    state = {'project': project, 'workspace': str(workspace)}

def save():
    checkpoint.write_text(json.dumps(state, indent=2) + '\n')

def api(method, path, body=None):
    req = urllib.request.Request(base + path, method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=20) as response:
        return json.load(response)

def wait(path, predicate):
    deadline = time.monotonic() + 360
    while time.monotonic() < deadline:
        value = api('GET', path)
        if predicate(value):
            return value
        if isinstance(value, dict):
            assert value.get('status') not in ('FAILED', 'CANCELLED', 'UNKNOWN'), value
        time.sleep(.5)
    raise TimeoutError('Inspect durable state before resuming: ' + path)

project = state['project']
save()
print('checkpoint:', checkpoint, flush=True)
if not state.get('created'):
    api('POST', '/projects', {'id': project, 'name': project, 'workspace': state['workspace']})
    state['created'] = True
    save()

def task(key, agent, prompt):
    if key not in state:
        t = api('POST', '/tasks', {'project_id': project, 'title': key,
            'description': prompt, 'assignee': agent, 'request_id': key})
        state[key] = t['id']
        save()
    t = api('GET', '/tasks/' + state[key])
    if t['status'] == 'BACKLOG':
        t = api('POST', '/tasks/' + t['id'] + '/actions', {'version': t['version'], 'action': 'ready'})
    if t['status'] != 'DONE':
        t = wait('/tasks/' + t['id'], lambda x: x['status'] == 'IN_REVIEW')
        assert key + '_OK' in t['result'], t
        t = api('POST', '/tasks/' + t['id'] + '/review', {'version': t['version'], 'accept': True})
    print(key, t['status'], flush=True)
    return t

auto = task('AUTONOMY', 'manager-autonomous', '''Bounded Factory autonomy test. Do not change files or create/delegate tasks. Read factory_decision_list. Call factory_decision_decide category technical, topic "local persistence", value "SQLite", rationale "One Core process owns state; no external service needed", request_id "autonomous-sqlite". Confirm authority policy and approved_by manager-autonomous. Retry that exact call with identical arguments and verify the same ID. Then deliberately call factory_decision_decide category product, topic "pricing", value "Paid subscriptions", rationale "Negative authorization test", request_id "forbidden-product" and confirm permission denied. Deliberately call factory_owner_ask body "Negative permission test; do not deliver", request_id "forbidden-owner" and confirm permission denied. These two expected denials are test assertions, not reasons to retry or escalate. Finish AUTONOMY_OK.''')
choices = api('GET', '/projects/' + project + '/decisions?status=approved')['items']
assert len(choices) == 1 and choices[0]['authority'] == 'policy' and choices[0]['approved_by'] == 'manager-autonomous', choices
assert choices[0]['category'] == 'technical' and choices[0]['config_hash'] and choices[0]['authority_rules']
assert not any(x['sender'] == 'manager-autonomous' for x in api('GET', '/inbox?project=' + project)), 'Autonomy or denied owner call created a notice'
if 'affected' not in state:
    affected = api('POST', '/tasks', {'project_id': project, 'title': 'Paid subscription implementation',
        'description': 'Do not execute until the pricing proposal has been approved by the owner.', 'assignee': 'demo', 'request_id': 'affected'})
    state['affected'] = affected['id']
    save()
producer = task('PROPOSAL', 'manager', '''Bounded owner rejection test. Do not change files or create/delegate tasks. Read current task list. Propose one decision category product, topic "pricing", value "Paid subscriptions", rationale "Evaluate a revenue model; pending owner review", request_id "pricing-proposal". Do not approve the proposal, start other tasks or ask questions. Finish PROPOSAL_OK.''')
if 'rejected' not in state:
    if 'rejection_body' not in state:
        proposed = api('GET', '/projects/' + project + '/decisions?status=proposed')['items']
        assert len(proposed) == 1, proposed
        d = proposed[0]
        state['proposal_id'] = d['id']
        state['rejection_body'] = {'version': d['version'], 'status': 'rejected', 'rationale':
            'Product must remain free and offline. Block unfinished task ' + state['affected'] +
            ' because paid subscriptions contradict this requirement. Propose an alternative pricing decision with value "Free offline app" and rationale explaining the revised plan; do not approve it yourself. Record a plan_deviation report confirming the task block and alternative proposal.', 'request_id': 'owner-reject'}
        save()
    body = state['rejection_body']
    state['rejected'] = api('POST', '/decisions/' + state['proposal_id'] + '/record', body)
    save()
rejected = state['rejected']
assert api('POST', '/decisions/' + rejected['id'] + '/record', state['rejection_body']) == rejected
message = wait('/messages/' + rejected['reaction_message_id'], lambda x: bool(x.get('delivery_run_id')))
revision = wait('/runs/' + message['delivery_run_id'], lambda x: x['status'] == 'COMPLETED')
affected = api('GET', '/tasks/' + state['affected'])
assert affected['status'] == 'BLOCKED', affected
alternatives = api('GET', '/projects/' + project + '/decisions?status=proposed')['items']
assert any(x['topic'] == 'pricing' and x['value'] == 'Free offline app' for x in alternatives), alternatives
assert api('GET', '/decisions/' + rejected['id'])['status'] == 'rejected'
reports = api('GET', '/projects/' + project + '/reports?kind=plan_deviation')['items']
assert any(x['run_id'] == revision['spec']['id'] for x in reports), reports
approved = api('GET', '/projects/' + project + '/decisions?status=approved')['items']
assert len(approved) == 1 and approved[0]['id'] == choices[0]['id'], approved
runs = api('GET', '/runs')
project_runs = [x for x in runs if x['spec']['project'] == project]
assert len(project_runs) == 3 and all(x['status'] == 'COMPLETED' for x in project_runs), project_runs
state['verification'] = {'autonomous_decision': choices[0]['id'], 'rejected_decision': rejected['id'],
    'revision_message': message['id'], 'revision_run': revision['spec']['id'], 'affected_task': affected['id'],
    'runs': [x['spec']['id'] for x in project_runs], 'result': 'PASS'}
save()
print(json.dumps(state['verification'], indent=2), flush=True)
