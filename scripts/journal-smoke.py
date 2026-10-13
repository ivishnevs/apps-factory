#!/usr/bin/env python3
"""Opt-in real Codex: reports, proposal persistence, explicit replacement, captured decisions."""
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
project = 'journal-smoke-' + uuid.uuid4().hex[:10]
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

def task(agent, title, prompt):
    t = api('POST', '/tasks', {'project_id': project, 'title': title, 'description': prompt,
        'assignee': agent, 'request_id': title})
    api('POST', '/tasks/' + t['id'] + '/actions', {'version': t['version'], 'action': 'ready'})
    t = wait('/tasks/' + t['id'], lambda x: x['status'] == 'IN_REVIEW')
    assert title + '_OK' in t['result'], t
    t = api('POST', '/tasks/' + t['id'] + '/review', {'version': t['version'], 'accept': True})
    return t, api('GET', '/tasks/' + t['id'] + '/attempts')[-1]

def approve(d, previous=None):
    body = {'version': d['version'], 'status': 'approved', 'rationale': 'Owner reviewed this exact project choice', 'request_id': 'approve-' + d['id']}
    if previous:
        body.update(supersedes_id=previous['id'], supersedes_version=previous['version'])
    path = '/decisions/' + d['id'] + '/record'
    resolved = api('POST', path, body)
    assert api('POST', path, body) == resolved
    return resolved

api('POST', '/projects', {'id': project, 'name': project, 'workspace': str(workspace)})
producer, attempt = task('demo', 'JOURNAL_PRODUCER', '''This is a bounded Factory journal test. Do not modify files or delegate or ask questions. First read factory_decision_list for topic "storage choice". Propose decision topic "storage choice", value "SQLite", rationale "Core is the sole state owner", request_id "propose-storage". Then create one report kind stage_completed, summary "Proposal prepared", purpose "Capture a significant project choice", changes ["Created storage choice proposal"], result "Proposal remains unapproved", needs_owner_action true, owner_action "Review the storage choice proposal", request_id "producer-report". Do not use decision_record or request action approval. Finish JOURNAL_PRODUCER_OK.''')
proposals = api('GET', '/projects/' + project + '/decisions?status=proposed')['items']
assert len(proposals) == 1, proposals
first = proposals[0]
assert first['created_by'] == 'demo' and first['run_id'] == attempt['spec']['id']
assert not api('GET', '/projects/' + project + '/context')['approved_decisions']
# Accepting the task never accepts its proposal. Reading inbox never accepts either.
inbox = api('GET', '/inbox?project=' + project)
assert len(inbox) == 2 and all(not x['blocking'] for x in inbox), inbox
for message in inbox:
    api('POST', '/inbox/' + message['id'] + '/read', {'version': message['version'], 'request_id': 'read-' + message['id']})
assert api('GET', '/decisions/' + first['id'])['status'] == 'proposed'
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
    assert api('GET', '/decisions/' + first['id']) == first
first = approve(first)
consumer, old_attempt = task('manager', 'JOURNAL_CONSUMER', '''This is a bounded journal test. Do not change files, create tasks, delegate or ask the owner anything. Verify the approved storage choice in your run context is SQLite. Call factory_project_context, factory_decision_list for topic "storage choice", and factory_decision_get on that approved decision; verify approved_by owner. Call factory_report_list with kind stage_completed and factory_report_get on the producer report; verify its run/task references. Read factory_project_feed with limit 5. Create one stage_completed report with summary "Registry context verified", purpose "Avoid repeating a resolved owner question", changes [], result "SQLite context and durable producer report verified", request_id "consumer-report". Finish JOURNAL_CONSUMER_OK.''')
assert old_attempt['spec']['decision_context'][0]['id'] == first['id'], old_attempt
replacement = api('POST', '/projects/' + project + '/decisions', {'topic': 'storage choice', 'value': 'PostgreSQL',
    'rationale': 'Future multi-node requirement', 'request_id': 'replacement'})
# A replacement without the current approved ID/version must fail atomically.
try:
    approve(replacement)
except urllib.error.HTTPError as error:
    assert error.code == 409, error
else:
    raise AssertionError('Silent overwrite was accepted')
second = approve(replacement, first)
assert api('GET', '/decisions/' + first['id'])['status'] == 'superseded'
revised, new_attempt = task('demo', 'JOURNAL_REVISED', '''This is a bounded journal context test. Do not modify files, delegate or ask questions. Verify the approved storage choice in your run context is PostgreSQL. Use factory_decision_list to check the current approved decision and factory_decision_get to read its supersedes_id. Verify there is exactly one approved storage choice. Create one stage_completed report summary "Replacement context verified", purpose "Use current project decisions", changes [], result "PostgreSQL is approved; old decision is superseded", request_id "revised-report". Finish JOURNAL_REVISED_OK.''')
assert new_attempt['spec']['decision_context'][0]['id'] == second['id'], new_attempt
assert api('GET', '/runs/' + old_attempt['spec']['id'])['spec']['decision_context'][0]['id'] == first['id']
wait('/runs', lambda rs: not any(r['spec']['project'] == project and r['status'] in ('QUEUED', 'RUNNING', 'CANCELLING', 'UNKNOWN') for r in rs))
all_reports = api('GET', '/projects/' + project + '/reports?limit=100')['items']
assert len([x for x in all_reports if x['source'] == 'agent']) == 3, all_reports
assert len([x for x in all_reports if x['kind'] == 'runtime' and x['result'] == 'COMPLETED']) == 3, all_reports
assert not api('GET', '/inbox?project=' + project)
summary = {'project': project, 'task_ids': [x['id'] for x in (producer, consumer, revised)],
    'run_ids': [x['spec']['id'] for x in (attempt, old_attempt, new_attempt)],
    'original_decision': first, 'replacement_decision': second, 'reports': all_reports, 'inbox_empty': True}
Path('.cache/reports-decisions-verification.json').write_text(json.dumps(summary, indent=2) + '\n')
print(json.dumps({'project': project, 'runs': summary['run_ids'], 'report_count': len(all_reports), 'decisions': [first['id'], second['id']], 'inbox_empty': True}, indent=2))
